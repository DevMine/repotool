//usr/bin/env go run $0 $@; exit
// Copyright 2014-2015 The DevMine authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package batch.go allows for batch processing several repositories with repotool.
// It is meant to insert a set of repositories data into a database, given
// a path containing several repositories.
// Depth where to find repositories from the given directory may be specified
// with the -d argument (which defaults to 0).
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

func main() {
	flag.Usage = func() {
		fmt.Printf("usage: %s [-d depth] [CONFIGURATION FILE] [REPOSITORIES ROOT FOLDER]\n",
			filepath.Base(os.Args[0]))
		flag.PrintDefaults()
		os.Exit(0)
	}
	depthflag := flag.Uint("d", 0, "depth level where to find repositories")
	maxGoroutines := flag.Uint("g", 4, "max number of goroutines to spawn")
	flag.Parse()

	if len(flag.Args()) != 2 {
		fmt.Fprintln(os.Stderr, "invalid # of arguments")
		flag.Usage()
	}

	configPath := flag.Arg(0)
	reposDir := flag.Arg(1)

	rtBin, err := exec.LookPath("repotool")
	if err != nil {
		fatal(err)
	}

	tasks := make(chan *exec.Cmd, 0)
	var wg sync.WaitGroup
	for w := uint(0); w < *maxGoroutines; w++ {
		wg.Add(1)
		go func() {
			for cmd := range tasks {
				out, err := cmd.CombinedOutput()
				fmt.Print(string(out))
				if err != nil {
					fmt.Println(err)
				}
			}
			wg.Done()
		}()
	}

	iterateRepos(tasks, rtBin, configPath, reposDir, *depthflag)

	close(tasks)
	wg.Wait()
}

func iterateRepos(tasks chan *exec.Cmd, rtBin, configPath, path string, depth uint) {
	fis, err := ioutil.ReadDir(path)
	if err != nil {
		fatal(err)
	}

	if depth == 0 {
		for _, fi := range fis {
			if !fi.IsDir() {
				continue
			}

			fmt.Println("adding repository: ", fi.Name(), " to the tasks pool")
			repoPath := filepath.Join(path, fi.Name())

			tasks <- exec.Command(rtBin, "-json=false", "-c", configPath, "-db", repoPath)
		}
		return
	}

	for _, fi := range fis {
		if !fi.IsDir() {
			continue
		}

		iterateRepos(tasks, rtBin, configPath, filepath.Join(path, fi.Name()), depth-1)
	}
}

func fatal(a ...interface{}) {
	fmt.Fprintln(os.Stderr, a...)
	os.Exit(1)
}
