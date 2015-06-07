PKG  = github.com/DevMine/repotool

all: check test build

install:
	go install ${PKG}/cmd/repotool
	go install ${PKG}/cmd/repotool-db

build:
	go build ${PKG}/cmd/repotool
	go build ${PKG}/cmd/repotool-db

test:
	go test ${PKG}/...

# FIXME: we shall compile libgit2 statically with git2go to prevent libgit2
# from being a dependency to run repotool
deps:
	go get -u github.com/golang/glog
	go get -u github.com/libgit2/git2go
	go get -u github.com/lib/pq
	go get -u github.com/spaolacci/murmur3
	go get -u -f github.com/DevMine/srcanlzr/src

dev-deps:
	go get -u github.com/golang/lint/golint

check:
	go vet ${PKG}/...
	golint ${PKG}/...

cover:
	go test -cover ${PKG}/...

clean:
	rm -f ./repotool ./repotool-db
