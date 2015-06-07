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
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync"

	"github.com/golang/glog"
	_ "github.com/lib/pq"
	mmh3 "github.com/spaolacci/murmur3"

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
		"hash",
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
		f, err := os.Create(*cpuprofile)
		if err != nil {
			fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if len(flag.Args()) != 1 {
		fmt.Fprintln(os.Stderr, "invalid # of arguments")
		flag.Usage()
	}

	if len(*configPath) == 0 {
		fatal(errors.New("a configuration file must be specified"))
	}

	var cfg *config.Config
	cfg, err = config.ReadConfig(*configPath)
	if err != nil {
		fatal(err)
	}

	// Make sure we finish writing logs before exiting.
	defer glog.Flush()

	var db *sql.DB
	db, err = openDBSession(*cfg.Database)
	if err != nil {
		return
	}
	defer db.Close()

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
		fatal(err)
	}

	if depth == 0 {
		for _, fi := range fis {
			if !fi.IsDir() {
				if filepath.Ext(fi.Name()) != ".tar" {
					continue
				}
			}

			repoPath := filepath.Join(path, fi.Name())
			fmt.Println("adding repository: ", repoPath, " to the tasks pool")
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

// fatal prints an error on standard error stream and exits.
func fatal(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
	os.Exit(1)
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

	repoID, err := getRepoID(db, r)
	if err != nil {
		return err
	}
	if repoID == nil {
		return errors.New("cannot find corresponding repository in database")
	}

	userIDs, err := getAllUsers(db)
	if err != nil {
		return err
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
		if err := insertCommit(userIDs, *repoID, c, tx, commitStmt, deltaStmt); err != nil {
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
func insertCommit(userIDs map[string]uint64, repoID uint64, c model.Commit, tx *sql.Tx, commitStmt, deltaStmt *sql.Stmt) error {
	authorID := userIDs[c.Author.Email]
	committerID := userIDs[c.Committer.Email]
	hash := genCommitHash(c)

	var commitID uint64
	err := commitStmt.QueryRow(
		repoID, authorID, committerID, hash,
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

// getAllUsers returns a map of all users IDs with their email address as keys.
func getAllUsers(db *sql.DB) (map[string]uint64, error) {
	rows, err := db.Query("SELECT id, email FROM users WHERE email IS NOT NULL AND email != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	userIDs := map[string]uint64{}
	for rows.Next() {
		var email string
		var id uint64
		if err := rows.Scan(&id, &email); err != nil {
			return nil, err
		}
		userIDs[email] = id
	}

	return userIDs, nil
}

// genCommitHash generates a hash (mmh3) from commit fields.
// This hash can then be used to uniquely identify a commit.
// Typically, we want to make sure not to insert twice the same commit into the
// database after an eventual second repotool run on the same repository.
func genCommitHash(c model.Commit) string {
	h := mmh3.New128()

	io.WriteString(h, c.VCSID)
	io.WriteString(h, c.Message)
	io.WriteString(h, c.Author.Name)
	io.WriteString(h, c.Author.Email)
	io.WriteString(h, c.Committer.Name)
	io.WriteString(h, c.Committer.Email)
	io.WriteString(h, c.AuthorDate.String())
	io.WriteString(h, c.CommitDate.String())
	io.WriteString(h, strconv.FormatInt(int64(c.FileChangedCount), 10))
	io.WriteString(h, strconv.FormatInt(int64(c.InsertionsCount), 10))
	io.WriteString(h, strconv.FormatInt(int64(c.DeletionsCount), 10))

	return hex.EncodeToString(h.Sum(nil))
}

// getRepoID returns the repository id of a repo in repositories table.
// If repo is not in the table, then 0 is returned.
func getRepoID(db *sql.DB, r repo.Repo) (*uint64, error) {
	if db == nil {
		return nil, errors.New("nil database given")
	}

	var id *uint64
	// Clone URL is unique
	err := db.QueryRow("SELECT id FROM repositories WHERE clone_url=$1", r.GetCloneURL()).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		return nil, nil
	case err != nil:
		return nil, err
	}
	return id, nil
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