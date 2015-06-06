// Copyright 2014-2015 The DevMine authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package repotool is able to fetch information from a source code repository.
// Typically, it can get all commits, their authors and commiters and so on
// and return this information in a JSON object. Alternatively, it is able
// to populate the information into a PostgreSQL database.
// Currently, on the Git VCS is supported.
package main

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/lib/pq"
	mmh3 "github.com/spaolacci/murmur3"

	"github.com/DevMine/srcanlzr/src"

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

func main() {
	var err error

	flag.Usage = func() {
		fmt.Printf("usage: %s [OPTION(S)] [REPOSITORY PATH]\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}

	configPath := flag.String("c", "", "configuration file")
	vflag := flag.Bool("v", false, "print version.")
	jsonflag := flag.Bool("json", true, "json output")
	dbflag := flag.Bool("db", false, "import data into the database")
	srctoolflag := flag.String("srctool", "", "read json file produced by srctool (give stdin to read from stdin)")
	flag.Parse()

	if *vflag {
		fmt.Printf("%s - %s\n", filepath.Base(os.Args[0]), version)
		os.Exit(0)
	}

	if len(flag.Args()) != 1 {
		fmt.Fprintln(os.Stderr, "invalid # of arguments")
		flag.Usage()
	}

	if *dbflag && len(*configPath) == 0 {
		fatal(errors.New("a configuration file must be specified when using db option"))
	}

	if !*jsonflag && (len(*srctoolflag) > 0) {
		fatal(errors.New("srctool flag may be used only in conjonction with json flag"))
	}

	var cfg *config.Config
	cfg, err = config.ReadConfig(*configPath)
	if err != nil {
		fatal(err)
	}

	repoPath := flag.Arg(0)
	var repository repo.Repo
	repository, err = repo.New(*cfg, repoPath)
	if err != nil {
		fatal(err)
	}
	defer func() {
		repository.Cleanup()
		if err != nil {
			fatal(err)
		}
	}()

	fmt.Fprintln(os.Stderr, "fetching repository commits...")
	tic := time.Now()
	err = repository.FetchCommits()
	if err != nil {
		return
	}
	toc := time.Now()
	fmt.Fprintln(os.Stderr, "done in ", toc.Sub(tic))

	if *jsonflag && (len(*srctoolflag) == 0) {
		var bs []byte
		bs, err = json.Marshal(repository)
		if err != nil {
			return
		}
		fmt.Println(string(bs))
	}

	if *jsonflag && (len(*srctoolflag)) > 0 {
		var r *bufio.Reader
		if *srctoolflag == strings.ToLower("stdin") {
			// read from stdin
			r = bufio.NewReader(os.Stdin)
		} else {
			// read from srctool json file
			var f *os.File
			if f, err = os.Open(*srctoolflag); err != nil {
				return
			}
			r = bufio.NewReader(f)
		}

		buf := new(bytes.Buffer)
		if _, err = io.Copy(buf, r); err != nil {
			fail(err)
			return
		}

		bs := buf.Bytes()
		var p *src.Project
		p, err = src.Unmarshal(bs)
		if err != nil {
			return
		}

		p.Repo = repository.GetRepository()
		bs, err = src.Marshal(p)
		if err != nil {
			return
		}

		fmt.Println(string(bs))
	}

	if *dbflag {
		var db *sql.DB
		db, err = openDBSession(cfg.Database)
		if err != nil {
			return
		}
		defer db.Close()

		fmt.Fprintf(os.Stderr,
			"inserting %d commits from %s repository into the database...\n",
			len(repository.GetCommits()), repository.GetName())
		tic := time.Now()
		err = insertRepoData(db, repository)
		toc := time.Now()
		if err != nil {
			return
		}
		fmt.Fprintln(os.Stderr, "done in ", toc.Sub(tic))
	}
}

// fatal prints an error on standard error stream and exits.
func fatal(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
	os.Exit(1)
}

// fail prints an error on standard error stream.
func fail(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
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
