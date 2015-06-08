// Copyright 2014-2015 The DevMine authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package repotool-db is able to fetch information from a source code repository.
// Typically, it can get all commits, their authors and commiters and so on
// and is able to populate the information into a PostgreSQL database.
// Currently, on the Git VCS is supported.
package main

import (
	"bytes"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"

	"github.com/golang/glog"
	_ "github.com/lib/pq"

	"github.com/DevMine/repotool/config"
	"github.com/DevMine/repotool/model"
	"github.com/DevMine/repotool/repo"
)

const version = "0.1.0"

// database fields per tables
var (
	diffDeltaFields = []string{
		"commit_id",
		"file_status",
		"is_file_binary",
		"similarity",
		"old_file_path",
		"new_file_path"}

	commitFields = []string{
		"repository_id",
		"author_id",
		"committer_id",
		"vcs_id",
		"message",
		"author_date",
		"commit_date",
		"file_changed_count",
		"insertions_count",
		"deletions_count"}
)

// program flags
var (
	configPath    = flag.String("c", "", "configuration file")
	vflag         = flag.Bool("V", false, "print version.")
	cpuprofile    = flag.String("cpuprofile", "", "write cpu profile to file")
	depthflag     = flag.Uint("d", 0, "depth level where to find repositories")
	numGoroutines = flag.Uint("g", uint(runtime.NumCPU()), "max number of goroutines to spawn")
)

// globals
var (
	userIDs = map[string]uint64{}
	repoIDs = map[string]uint64{}
)

func main() {
	var err error

	flag.Usage = func() {
		fmt.Printf("usage: %s [OPTION(S)] [REPOSITORIES ROOT FOLDER]\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}
	flag.Parse()

	if *vflag {
		fmt.Printf("%s - %s\n", filepath.Base(os.Args[0]), version)
		os.Exit(0)
	}

	if *cpuprofile != "" {
		var f *os.File
		f, err = os.Create(*cpuprofile)
		if err != nil {
			glog.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if len(flag.Args()) != 1 {
		fmt.Fprintln(os.Stderr, "invalid # of arguments")
		flag.Usage()
	}

	if len(*configPath) == 0 {
		glog.Fatal(errors.New("a configuration file must be specified"))
	}

	var cfg *config.Config
	cfg, err = config.ReadConfig(*configPath)
	if err != nil {
		glog.Fatal(err)
	}

	// Make sure we finish writing logs before exiting.
	defer glog.Flush()

	var db *sql.DB
	db, err = openDBSession(*cfg.Database)
	if err != nil {
		return
	}
	defer func() {
		db.Close()
		if err != nil {
			glog.Fatal(err)
		}
	}()

	var empty bool
	if empty, err = isTableEmpty(db, "commits"); !empty || (err != nil) {
		if err == nil {
			err = errors.New("commits table is not empty")
		}
		return
	}

	if err = fetchAllUsers(db); err != nil {
		return
	}
	if err = fetchAllRepos(db); err != nil {
		return
	}

	reposPath := make(chan string, 0)
	var wg sync.WaitGroup
	for w := uint(0); w < *numGoroutines; w++ {
		wg.Add(1)
		go func() {
			for path := range reposPath {
				work := func() error {
					repository, err := repo.New(*cfg, path)
					if err != nil {
						return err
					}
					defer repository.Cleanup()

					if err = repository.FetchCommits(); err != nil {
						return err
					}

					if err = insertRepoData(db, repository); err != nil {
						return err
					}
					return nil
				}
				if err := work(); err != nil {
					glog.Error(err)
				}
			}
			wg.Done()
		}()
	}

	reposDir := flag.Arg(0)
	iterateRepos(reposPath, reposDir, *depthflag)

	close(reposPath)
	wg.Wait()

}

func iterateRepos(reposPath chan string, path string, depth uint) {
	fis, err := ioutil.ReadDir(path)
	if err != nil {
		glog.Fatal(err)
	}

	if depth == 0 {
		for _, fi := range fis {
			if !fi.IsDir() {
				if filepath.Ext(fi.Name()) != ".tar" {
					continue
				}
			}

			repoPath := filepath.Join(path, fi.Name())
			glog.Info("adding repository:", repoPath, "to the pool")
			reposPath <- repoPath
		}
		return
	}

	for _, fi := range fis {
		if !fi.IsDir() {
			continue
		}

		iterateRepos(reposPath, filepath.Join(path, fi.Name()), depth-1)
	}
}

// openDBSession creates a session to the database.
func openDBSession(cfg config.DatabaseConfig) (*sql.DB, error) {
	dbURL := fmt.Sprintf(
		"user='%s' password='%s' host='%s' port=%d dbname='%s' sslmode='%s'",
		cfg.UserName, cfg.Password, cfg.HostName, cfg.Port, cfg.DBName, cfg.SSLMode)

	return sql.Open("postgres", dbURL)
}

// insertRepoData inserts repository data into the database, or updates it
// if it is already there.
func insertRepoData(db *sql.DB, r repo.Repo) error {
	if db == nil {
		return errors.New("nil database given")
	}

	repoID, ok := repoIDs[r.GetCloneURL()]
	if !ok {
		return errors.New("cannot find corresponding repository in database")
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	commitStmt, err := tx.Prepare(genInsQuery("commits", commitFields...) + " RETURNING id")
	if err != nil {
		return err
	}

	deltaStmt, err := tx.Prepare(genInsQuery("commit_diff_deltas", diffDeltaFields...))
	if err != nil {
		return err
	}

	for _, c := range r.GetCommits() {
		if err := insertCommit(repoID, c, tx, commitStmt, deltaStmt); err != nil {
			return err
		}
	}

	if err := commitStmt.Close(); err != nil {
		return err
	}
	if err := deltaStmt.Close(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	return nil
}

// insertCommit inserts a commit into the database
func insertCommit(repoID uint64, c model.Commit, tx *sql.Tx, commitStmt, deltaStmt *sql.Stmt) error {
	authorID := userIDs[c.Author.Email]
	committerID := userIDs[c.Committer.Email]

	var commitID uint64
	err := commitStmt.QueryRow(
		repoID, authorID, committerID,
		c.VCSID, c.Message, c.AuthorDate, c.CommitDate,
		c.FileChangedCount, c.InsertionsCount, c.DeletionsCount).Scan(&commitID)
	if err != nil {
		return err
	}

	for _, d := range c.DiffDelta {
		if err := insertDiffDelta(commitID, d, deltaStmt); err != nil {
			return err
		}
	}

	return nil
}

// insertDiffDelta inserts a commit diff delta into the database.
func insertDiffDelta(commitID uint64, d model.DiffDelta, stmt *sql.Stmt) error {
	_, err := stmt.Exec(commitID, d.Status, d.Binary, d.Similarity, d.OldFilePath, d.NewFilePath)
	if err != nil {
		return err
	}
	return nil
}

// fetchAllUsers fetch users IDs and put them into the userIDs global hashmap
// with their email address as keys.
func fetchAllUsers(db *sql.DB) error {
	rows, err := db.Query("SELECT id, email FROM users WHERE email IS NOT NULL AND email != ''")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var email string
		var id uint64
		if err := rows.Scan(&id, &email); err != nil {
			return err
		}
		userIDs[email] = id
	}

	return nil
}

func fetchAllRepos(db *sql.DB) error {
	rows, err := db.Query("SELECT id, clone_url FROM repositories")
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id uint64
		var cloneURL string
		if err := rows.Scan(&id, &cloneURL); err != nil {
			return err
		}
		repoIDs[cloneURL] = id
	}

	return nil
}

// genInsQuery generates a query string for an insertion in the database.
func genInsQuery(tableName string, fields ...string) string {
	var buf bytes.Buffer

	buf.WriteString(fmt.Sprintf("INSERT INTO %s(%s)\n",
		tableName, strings.Join(fields, ",")))
	buf.WriteString("VALUES(")

	for ind := range fields {
		if ind > 0 {
			buf.WriteString(",")
		}

		buf.WriteString(fmt.Sprintf("$%d", ind+1))
	}

	buf.WriteString(")\n")

	return buf.String()
}

// isTableEmpty returns true of the table tableName is empty, false otherwise.
func isTableEmpty(db *sql.DB, tableName string) (bool, error) {
	var state bool
	err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM " + tableName + " LIMIT 1)").Scan(&state)
	return !state, err
}
