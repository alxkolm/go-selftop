This tool listen for user activity throughout [xrecord-echo](https://github.com/alxkolm/rust-xrecord-echo) and store user activity to database.

It records keys, clicks, mouse move, windows titles and processes. Than this data will be visualized by [php-selftop](https://github.com/alxkolm/php-selftop).

## Install

### Prerequisites
- Go compiler ([Installing Go](https://golang.org/doc/install))
- sqlite3

Clone and run:

    go get github.com/gdamore/mangos github.com/mitchellh/go-homedir github.com/mattn/go-sqlite3
    go build -o selftop

## Run

Just run executable:

    ./selftop

But I recommend use supervisor like [*runit*](http://smarden.org/runit/) to manage process (run on system startup and restart on crash).
