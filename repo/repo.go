// Copyright 2014-2015 The DevMine Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package repo provides functions for accessing VCS information.
package repo

import (
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DevMine/repotool/config"
	"github.com/DevMine/repotool/model"
	"github.com/DevMine/repotool/repo/git"
)

// Repository types.
const (
	Git = "git"
	Hg  = "mercurial"
	SVN = "subversion"
	Bzr = "bazaar"
	CVS = "cvs"
)

// suppVCS is a list if supported VCS.
var suppVCS = []string{
	Git,
}

// Repo interface defines what needs to be implemented to construct a Repo object.
type Repo interface {
	// fetchCommits populates Commits attribute with all commits of a repository.
	FetchCommits() error

	// GetRepository returns a repository structure from a repo.
	GetRepository() *model.Repository

	// GetName returns the name of a repo.
	GetName() string

	// GetVCS returns the VCS typoe of a repo.
	GetVCS() string

	// GetCloneURL returns the clone URL of a repo.
	GetCloneURL() string

	// GetClonePath returns the clone path of a repo.
	GetClonePath() string

	// GetDefaultBranch returns the default branch of a repo.
	GetDefaultBranch() string

	// GetCommits returns the list of commits of a repo.
	GetCommits() []model.Commit
}

var _ Repo = (*git.GitRepo)(nil)

// New creates a new Repo object.
func New(cfg config.DataConfig, path string) (Repo, error) {
	// check for git repo
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		cloneURL, err := extractGitURL(path)
		if err != nil {
			return nil, err
		}

		branch, err := extractGitDefaultBranch(path)
		if err != nil {
			return nil, err
		}

		repository := model.Repository{
			Name:          extractName(path),
			VCS:           string(Git),
			CloneURL:      *cloneURL,
			ClonePath:     path,
			DefaultBranch: *branch,
		}

		gitRepo, err := git.New(cfg, repository)
		if err != nil {
			return nil, err
		}

		return gitRepo, nil
	}
	return nil, errors.New("unsupported repository type")
}

// extractName extracts to name of a repository given its clone URL.
func extractName(path string) string {
	return filepath.Base(path)
}

// extractGitURL returns a git repository clone URL as a string, given the
// path to its location on disk.
func extractGitURL(path string) (*string, error) {
	f, err := os.Open(filepath.Join(path, ".git", "config"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	bs, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile("url ?= ?(.+)")
	match := re.FindStringSubmatch(string(bs))
	if len(match) != 2 {
		return nil, errors.New("invalid git config file")
	}

	if len(match[1]) == 0 {
		return nil, errors.New("cannot extract git clone url")
	}

	return &match[1], nil
}

// extractGitDefaultBranch returns the branch to which HEAD of a git
// repository is pointing at.
func extractGitDefaultBranch(path string) (*string, error) {
	f, err := os.Open(filepath.Join(path, ".git", "HEAD"))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	bs, err := ioutil.ReadAll(f)
	if err != nil {
		return nil, err
	}

	re := regexp.MustCompile("ref: refs\\/heads\\/[a-zA-Z0-9].+")
	match := re.FindString(string(bs))
	if len(match) == 0 {
		return nil, errors.New("no branch (detached HEAD state)")
	}

	index := strings.LastIndex(match, "/")
	branch := string(match[index+1:])
	if len(branch) == 0 {
		// we shall not be able to reach here but just in case
		return nil, errors.New("no branch (detached HEAD state)")
	}

	return &branch, nil
}
