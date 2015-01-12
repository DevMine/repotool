// Copyright 2014-2015 The DevMine authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package model

// Repository represents a source code repository.
type Repository struct {
	Name          string   `json:"name"`
	VCS           string   `json:"vcs"`
	CloneURL      string   `json:"clone_url"`
	ClonePath     string   `json:"clone_path"`
	DefaultBranch string   `json:"default_branch"`
	Commits       []Commit `json:"commits"`
}
