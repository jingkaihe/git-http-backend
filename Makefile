NAME=git-http-backend

COMMIT=$(shell git rev-parse --short HEAD)
VERSION=$(shell cat VERSION)

LDFLAGS=-ldflags "-w -X main.VERSION=${VERSION} -X main.COMMIT=${COMMIT}"

all:
	go build ${LDFLAGS} -o ${NAME}

.PHONY: clean
clean:
	if [ -f ${NAME} ] ; then rm ${NAME} ; fi
