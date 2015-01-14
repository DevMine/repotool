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

	"github.com/DevMine/repotool/config"
	"github.com/DevMine/repotool/model"
	"github.com/DevMine/repotool/repo"
)

const version = "0.1.0"

func main() {
	flag.Usage = func() {
		fmt.Printf("usage: %s [OPTION(S)] [REPOSITORY PATH]\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(0)
	}

	configPath := flag.String("c", "", "configuration file")
	vflag := flag.Bool("v", false, "print version.")
	jsonflag := flag.Bool("json", true, "json output")
	dbflag := flag.Bool("db", false, "import data into the database")
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

	cfg, err := config.ReadConfig(*configPath)
	if err != nil {
		fatal(err)
	}

	repoPath := flag.Arg(0)
	repository, err := repo.New(cfg.Data, repoPath)
	if err != nil {
		fatal(err)
	}

	fmt.Fprintln(os.Stderr, "fetching repository commits...")
	tic := time.Now()
	err = repository.FetchCommits()
	if err != nil {
		fatal(err)
	}
	toc := time.Now()
	fmt.Fprintln(os.Stderr, "done in ", toc.Sub(tic))

	if *jsonflag {
		bs, err := json.Marshal(repository)
		if err != nil {
			fatal(err)
		}

		fmt.Println(string(bs))
	}

	if *dbflag {
		db, err := openDBSession(cfg.Database)
		if err != nil {
			fatal(err)
		}
		defer db.Close()

		fmt.Fprintf(os.Stderr, "inserting %d commits into the database...\n",
			len(repository.GetCommits()))
		tic := time.Now()
		insertRepoData(db, repository)
		toc := time.Now()
		fmt.Fprintln(os.Stderr, "done in ", toc.Sub(tic))
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
func insertRepoData(db *sql.DB, r repo.Repo) {
	if db == nil {
		fatal(errors.New("nil database given"))
	}

	repoID := getRepoID(db, r)
	if repoID == 0 {
		fatal(errors.New("no corresponding repository found in the database: impossible to insert data"))
	}

	for _, c := range r.GetCommits() {
		insertCommit(db, repoID, c)
	}
}

// insertCommit inserts a commit into the database
func insertCommit(db *sql.DB, repoID int, c model.Commit) {
	commitFields := []string{
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

	authorID := getUserID(db, c.Author.Email)
	committerID := getUserID(db, c.Committer.Email)
	hash := genCommitHash(c)

	var commitID int64
	query := genInsQuery("commits", commitFields...)
	err := db.QueryRow(query+" RETURNING id",
		repoID, authorID, committerID, hash,
		c.VCSID, c.Message, c.AuthorDate, c.CommitDate,
		c.FileChangedCount, c.InsertionsCount, c.DeletionsCount).Scan(&commitID)
	if err != nil {
		fatal(err)
	}

	for _, d := range c.DiffDelta {
		insertDiffDelta(db, commitID, d)
	}
}

// insertDiffDelta inserts a commit diff delta into the database.
func insertDiffDelta(db *sql.DB, commitID int64, d model.DiffDelta) {
	diffDeltaFields := []string{
		"commit_id",
		"file_status",
		"is_file_binary",
		"similarity",
		"old_file_path",
		"new_file_path"}

	query := genInsQuery("commit_diff_deltas", diffDeltaFields...)
	_, err := db.Exec(query,
		commitID, d.Status, d.Binary, d.Similarity, d.OldFilePath, d.NewFilePath)
	if err != nil {
		fatal(err)
	}
}

// genCommitHash generates a hash (mmh3) from commit fields.
// This hash can then be used to uniquely identify a commit.
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
func getRepoID(db *sql.DB, r repo.Repo) int {
	if db == nil {
		fatal(errors.New("nil database given"))
	}

	var id int
	// Clone URL is unique
	err := db.QueryRow("SELECT id FROM repositories WHERE clone_url=$1", r.GetCloneURL()).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		return 0
	case err != nil:
		fatal(err)
	}
	return id
}

// getUserID attempts to find a user ID given its email address.
// Email addresses are unique, however they may not be provided.
// If no user ID is found, nil is returned, otherwhise the user ID
// is returned.
func getUserID(db *sql.DB, email string) *int {
	if db == nil {
		fatal(errors.New("nil database given"))
	}

	if len(email) == 0 {
		return nil
	}

	var id *int
	err := db.QueryRow("SELECT id FROM users WHERE email=$1", email).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		return nil
	case err != nil:
		fatal(err)
	}
	return id
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
