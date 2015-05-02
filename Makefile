PKG  = github.com/DevMine/repotool
EXEC = repotool

all: check test build

install:
	go install ${PKG}

build:
	go build -o ${EXEC} ${PKG}

test:
	go test ${PKG}/...

# FIXME: we shall compile libgit2 statically with git2go to prevent libgit2
# from being a dependency to run repotool
deps:
	go get -u github.com/libgit2/git2go
	go get -u github.com/lib/pq
	go get -u github.com/spaolacci/murmur3
	go get -u github.com/DevMine/srcanlzr/src

dev-deps:
	go get -u github.com/golang/lint/golint

check:
	go vet ${PKG}/...
	golint ${PKG}/...

cover:
	go test -cover ${PKG}/...

clean:
	rm -f ./${EXEC}
