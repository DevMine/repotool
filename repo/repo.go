// Copyright 2014-2015 The DevMine Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package repo provides functions for accessing VCS information.
package repo

import (
	"archive/tar"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DevMine/repotool/config"
	"github.com/DevMine/repotool/model"
)

// Repository types.
const (
	Git = "git"
	Hg  = "mercurial"
	SVN = "subversion"
	Bzr = "bazaar"
	CVS = "cvs"
)

// suppVCS is a list of supported VCS.
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

var _ Repo = (*gitRepo)(nil)

// New creates a new Repo object.
func New(cfg config.Config, path string) (Repo, error) {
	var repo Repo

	// check for git repo
	if vcs, err := detectVCS(path); err == nil {

		tmpPath := path
		if strings.HasSuffix(path, ".tar") {
			tmpPath, err = untarGitFolder(cfg.TmpDir, path)
			if err != nil {
				return nil, err
			}
			path = strings.TrimSuffix(path, ".tar")
		}
		cloneURL, err := extractGitURL(tmpPath)
		if err != nil {
			return nil, err
		}

		branch, err := extractGitDefaultBranch(tmpPath)
		if err != nil {
			return nil, err
		}

		repository := model.Repository{
			Name:          extractName(path),
			VCS:           vcs,
			CloneURL:      *cloneURL,
			ClonePath:     path,
			DefaultBranch: *branch,
		}
		repo, err = newGitRepo(cfg, repository, tmpPath)
		if err != nil {
			return nil, err
		}

		return repo, nil
	}

	return nil, errors.New("unsupported repository type")
}

// detect vcs used
func detectVCS(path string) (string, error) {
	if strings.HasSuffix(path, ".tar") {
		archiveFile, err := os.Open(path)
		if err != nil {
			return "", err
		}
		defer archiveFile.Close()

		tr := tar.NewReader(archiveFile)

		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			if strings.Contains(hdr.Name, ".git") {
				return Git, nil
			}
		}
	} else if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return Git, nil
	}

	return "", errors.New("VCS type not found")
}

// extractName extracts to name of a repository given its clone URL.
func extractName(path string) string {
	return filepath.Base(path)
}
