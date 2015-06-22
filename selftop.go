package main

import (
    "fmt"
    "github.com/gdamore/mangos"
    // "github.com/gdamore/mangos/protocol/pub"
    "github.com/gdamore/mangos/protocol/sub"
    // "github.com/gdamore/mangos/transport/ipc"
    "github.com/gdamore/mangos/transport/tcp"
    "os"
    "strings"
    "strconv"
    // "time"
    "database/sql"
    _ "github.com/go-sql-driver/mysql"
)

var sock mangos.Socket

type EventType uint

const (
    MotionEvent  EventType = 1 << iota
    EnterEvent   EventType = 1 << iota
    KeyEvent     EventType = 1 << iota
    ClickEvent   EventType = 1 << iota
    UnknownEvent EventType = 1 << iota
)
type Window struct {
    title string
    class string
}
type Event struct {
    eventType EventType
    window    Window
    time      uint
}

type Counter struct {
    motions uint
    clicks  uint
    keys    uint
    time    uint
}


var prevEvent Event
var counter Counter
var db *sql.DB
// Map windows to ID in DB
var windows map[Window]int64
var insertWindowCommand *sql.Stmt
var selectWindowCommand *sql.Stmt
var insertMetricsCommand *sql.Stmt

func main() {
    var err error
    var msg []byte

	createSocket()
    
    bootstrapData()

    for {
        if msg, err = sock.Recv(); err != nil {
            die("Cannot recv: %s", err.Error())
        }
        event := parseMessage(string(msg))
        processEvent(event)
    }
}


func createSocket() {
    var err error
    var url = "tcp://127.0.0.1:1234"

    if sock, err = sub.NewSocket(); err != nil {
        die("can't get new sub socket: %s", err.Error())
    }
    sock.AddTransport(tcp.NewTransport())
    if err = sock.Dial(url); err != nil {
        die("can't dial on sub socket: %s", err.Error())
    }
    // Empty byte array effectively subscribes to everything
    err = sock.SetOption(mangos.OptionSubscribe, []byte(""))
    if err != nil {
        die("cannot subscribe: %s", err.Error())
    }
}

func die(format string, v ...interface{}) {
    fmt.Fprintln(os.Stderr, fmt.Sprintf(format, v...))
    os.Exit(1)
}

func parseMessage(message string) (event Event) {
    var parts = strings.Split(message, "\n")
    window := Window {
        title: parts[3],
        class: parts[4],
    }
    var eventType EventType

    switch parts[1] {
    case "MotionEvent":
        eventType = MotionEvent
    case "EnterEvent":
        eventType = EnterEvent
    case "KeyEvent":
        eventType = KeyEvent
    case "ClickEvent":
        eventType = ClickEvent
    default:
        eventType = UnknownEvent
    }
    time, _ := strconv.ParseInt(parts[2], 0, 0)

    return Event {
        eventType: eventType,
        window:    window,
        time:      uint(time),
    }
}

func processEvent(event Event) {
    defer func(){prevEvent = event}()
    windowId := processWindow(event.window)
    if prevEvent.time == 0 {
        return
    }

    delta := event.time - prevEvent.time
    counter.time += delta

    switch event.eventType {
    case MotionEvent:
        // filter mouse motions
        if prevEvent.eventType != MotionEvent || delta > 200 {
            counter.motions += 1
        }
    case ClickEvent:
        counter.clicks += 1
    case KeyEvent:
        counter.keys += 1
    }

    if prevEvent.window != event.window {
        // TODO save record to DB for window from prevEvent
        insertMetricsCommand.Exec(
            windowId,
            counter.time,
            counter.motions,
            counter.clicks,
            counter.keys)
        // reset counter
        counter = Counter {
            motions: 0,
            clicks:  0,
            keys:    0,
            time:    0,
        }
    }
    
    fmt.Printf("Window %s time %.3f sec motions: %d, clicks: %d, keys: %d\n", event.window.class, float32(counter.time)/1000, counter.motions, counter.clicks, counter.keys)
}

func processWindow(window Window) int64 {
    if _, ok := windows[window]; !ok {
        // try to find window in db
        var id int64
        err := selectWindowCommand.QueryRow(window.title, window.class).Scan(&id)
        switch err {
        case sql.ErrNoRows:
            // store window to db
            res, _ := insertWindowCommand.Exec(window.title, window.class)
            id, _ = res.LastInsertId()
            windows[window] = id
        default:
            windows[window] = id
        }   
    }
    return windows[window]
}

func bootstrapData() {
    windows = make(map[Window]int64)
    var err error

    db, err = sql.Open("mysql", "root@/selftop")
    if err != nil {
        panic("Could not create db connection")
    }
    
    insertWindowCommand, err = db.Prepare(
        "INSERT INTO window (title, class) VALUES (?,?)")
    if err != nil {
        panic("Could not create prepared statement." + err.Error())
    }

    selectWindowCommand, err = db.Prepare(
        "SELECT id FROM window WHERE title = ? AND class = ? LIMIT 1")
    if err != nil {
        panic("Could not create prepared statement." + err.Error())
    }

    insertMetricsCommand, err = db.Prepare(
        "INSERT INTO metrics SET window_id = ?, time = ?, motions = ?, clicks = ?, `keys` = ?")
    if err != nil {
        panic("Could not create prepared statement." + err.Error())
    }
}