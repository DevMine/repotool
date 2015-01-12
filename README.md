# repotool - a tool to aggregate source code repositories metadata

`repotool` is a command line tool that aggregates source code repositories
metadata (such as VCS type, commits and so on) and produces JSON objects out of
it.  It is also able to store repository information into a database.

Currently, only [git](http://git-scm.com/) is supported.

Below is an example of the data produced:

```
{
  "name": "crawld",
  "vcs": "git",
  "clone_url": "https://github.com/DevMine/crawld.git",
  "clone_path": "/home/robin/Hacking/crawld",
  "default_branch": "master",
  "commits": [
    {
      "vcs_id": "ba79fdaf602df3330d34dc9f8c4e581e324dbc92",
      "message": "doc: add missing package documentation\n",
      "author": {
        "name": "Kevin Gillieron",
        "email": "kevin.gillieron@gw-computing.net"
      },
      "committer": {
        "name": "Kevin Gillieron",
        "email": "kevin.gillieron@gw-computing.net"
      },
      "author_date": "2015-01-05T17:14:42+01:00",
      "commit_date": "2015-01-05T17:14:49+01:00",
      "file_changed_count": 4,
      "insertions_count": 6,
      "deletions_count": 0
    },
    {
      "vcs_id": "12ae97c091bb8cdeabf89026eefac428819aaed1",
      "message": "crawlers/github: Add function to check a repository structure.\n\nSome fields of a repository structure are mandatory to us, namely:\n\n- ID\n- Name\n- Language\n- CloneURL\n- Owner (and Owner.Login)\n- Fork\n\nThese fields are required either because their related column in the\ndatabase does not allow NULL values or because they are required for\nfurther API calls. All other fields may be nil.\n",
      "author": {
        "name": "Robin Hahling",
        "email": "robin.hahling@gw-computing.net"
      },
      "committer": {
        "name": "Robin Hahling",
        "email": "robin.hahling@gw-computing.net"
      },
      "author_date": "2015-01-05T16:32:49+01:00",
      "commit_date": "2015-01-05T16:38:41+01:00",
      "file_changed_count": 1,
      "insertions_count": 83,
      "deletions_count": 0
    },
    ...
  ]
}
```

## Installation

`repotool` depends on [git2go](https://github.com/libgit2/git2go), which is a
[Go](http://golang.org/) binding to [libgit2](https://libgit2.github.com/), a C
library that implements `git` core methods.

`git2go` needs to be installed as described in their
[README.md](https://github.com/libgit2/git2go/blob/master/README.md#installing)
in order to statically link `libgit2` to the `git2go` package.

To install `repotool`, run this command in a terminal, assuming
[Go](http://golang.org/) is installed:

    go get github.com/DevMine/repotool

Or you can download a binary for your platform from the DevMine project's
[downloads page](http://devmine.ch/downloads).

## Usage

`repotool` produces JSON, provided that you feed it with a path to a source code
repository. By default, informative messages are outputted to `stderr` whereas
JSON is outputted to `stdout`. Example usage:

    repotool ~/Code/myawesomeproject > myawesomeproject.json

To insert data into the PostgreSQL database, you need to provide a configuration
file in argument. Simply copy `repotool.conf.sample` to `repotool.conf` and
adjust database connection information. See this
[README.md](https://github.com/DevMine/repotool/blob/master/db/README.md) for
more information about the database schema. Example usage:

    repotool -json=false -db -c repotool.conf ~/Code/myawesomeproject
