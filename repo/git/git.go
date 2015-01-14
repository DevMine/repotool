// Copyright 2014-2015 The DevMine authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package git implements the repo interface to handle the Git VCS.
package git

import (
	g2g "github.com/libgit2/git2go"

	"github.com/DevMine/repotool/config"
	"github.com/DevMine/repotool/model"
)

// GitRepo is a repository with some things specific to git.
type GitRepo struct {
	model.Repository
	cfg config.DataConfig
	r   *g2g.Repository
}

// New creates a new GitRepo object.
func New(cfg config.DataConfig, repository model.Repository) (*GitRepo, error) {
	r, err := g2g.OpenRepository(repository.ClonePath)
	if err != nil {
		return nil, err
	}

	return &GitRepo{Repository: repository, cfg: cfg, r: r}, nil
}

// FetchCommits fetches all commits from a Git repository and adds them to
// the list of commits of the repository object.
func (gr *GitRepo) FetchCommits() error {
	gr.Commits = make([]model.Commit, 0) // give number of commits

	rw, err := gr.r.Walk()
	if err != nil {
		return err
	}

	err = rw.PushHead()
	if err != nil {
		return err
	}

	err = rw.Iterate(gr.addCommit)
	if err != nil {
		return err
	}

	return nil
}

// GetName returns the name of a git repository.
func (gr GitRepo) GetName() string {
	return gr.Name
}

// GetVCS returns the VCS type (shall be "git").
func (gr GitRepo) GetVCS() string {
	return gr.VCS
}

// GetCloneURL returns the git repository clone URL.
func (gr GitRepo) GetCloneURL() string {
	return gr.CloneURL
}

// GetClonePath returns the clone path of a git repository.
func (gr GitRepo) GetClonePath() string {
	return gr.ClonePath
}

// GetDefaultBranch returns the git repository default branch.
func (gr GitRepo) GetDefaultBranch() string {
	return gr.DefaultBranch
}

// GetCommits returns the list of commits in the git repository.
// If the list is empty of nil, this probably means that a call to
// FetchCommits() is needed to populate the list.
func (gr GitRepo) GetCommits() []model.Commit {
	return gr.Commits
}

var deltaMap = map[g2g.Delta]*string{
	g2g.DeltaUnmodified: nil,
	g2g.DeltaAdded:      &model.StatusAdded,
	g2g.DeltaDeleted:    &model.StatusDeleted,
	g2g.DeltaModified:   &model.StatusModified,
	g2g.DeltaRenamed:    &model.StatusRenamed,
	g2g.DeltaCopied:     &model.StatusCopied,
	g2g.DeltaIgnored:    nil,
	g2g.DeltaUntracked:  nil,
	g2g.DeltaTypeChange: nil,
}

// addCommit is conform to the g2g.RevWalIterator type in order to be used
// by the g2g.Iterate() function to iterate over all commits of a Git repository.
func (gr *GitRepo) addCommit(c *g2g.Commit) bool {
	if c == nil {
		return false
	}

	var commit model.Commit

	oID := c.Id()
	if oID == nil {
		return false
	}
	commit.VCSID = oID.String()

	commit.Message = c.Message()

	var author model.Developer
	author.Name = c.Author().Name
	author.Email = c.Author().Email
	commit.Author = author

	var committer model.Developer
	committer.Name = c.Committer().Name
	committer.Email = c.Committer().Email
	commit.Committer = committer

	commit.CommitDate = c.Committer().When
	commit.AuthorDate = c.Author().When

	parentC := c.Parent(0)
	if parentC == nil {
		return false
	}

	parentTree, err := parentC.Tree()
	if err != nil {
		return false
	}

	cTree, err := c.Tree()
	if err != nil {
		return false
	}

	diffOpts, err := g2g.DefaultDiffOptions()
	if err != nil {
		return false
	}

	diff, err := gr.r.DiffTreeToTree(parentTree, cTree, &diffOpts)
	if err != nil {
		return false
	}

	stats, err := diff.Stats()
	if err != nil {
		return false
	}

	nDeltas, err := diff.NumDeltas()
	if err != nil {
		return false
	}

	if gr.cfg.CommitDeltas {
		for d := 0; d < nDeltas; d++ {
			var cdd model.DiffDelta

			if gr.cfg.CommitPatches {
				patch, err := diff.Patch(d)
				if err != nil || patch == nil {
					return false
				}
				p, err := patch.String()
				if err != nil {
					return false
				}
				cdd.Patch = &p
			}

			diffDelta, err := diff.GetDelta(d)
			if err != nil {
				return false
			}
			cdd.Status = deltaMap[diffDelta.Status]

			var isBin bool
			if (diffDelta.Flags & g2g.DiffFlagBinary) > 0 {
				isBin = true
			}
			cdd.Binary = &isBin

			// TODO compute similarity to add to cdd.Similarity

			cdd.OldFilePath = &diffDelta.OldFile.Path
			cdd.NewFilePath = &diffDelta.NewFile.Path

			commit.DiffDelta = append(commit.DiffDelta, cdd)
		}
	}

	commit.FileChangedCount = stats.FilesChanged()
	commit.InsertionsCount = stats.Insertions()
	commit.DeletionsCount = stats.Deletions()

	gr.Commits = append(gr.Commits, commit)

	return true
}
