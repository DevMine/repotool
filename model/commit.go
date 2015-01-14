// Copyright 2014-2015 The DevMine authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package model

import "time"

// Commit is a representation of a VCS commit.
type Commit struct {
	VCSID            string    `json:"vcs_id"`
	Message          string    `json:"message"`
	Author           Developer `json:"author"`
	Committer        Developer `json:"committer"`
	AuthorDate       time.Time `json:"author_date"`
	CommitDate       time.Time `json:"commit_date"`
	Patches          []string  `json:"patches,omitempty"`
	FileChangedCount int       `json:"file_changed_count"`
	InsertionsCount  int       `json:"insertions_count"`
	DeletionsCount   int       `json:"deletions_count"`
}
