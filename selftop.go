package main

import (
    "fmt"
    "github.com/go-mangos/mangos"
    "github.com/go-mangos/mangos/protocol/sub"
    "github.com/go-mangos/mangos/transport/tcp"
    "github.com/mitchellh/go-homedir"
    "os"
    "strings"
    "time"
    "database/sql"
    _ "github.com/mattn/go-sqlite3"
    "encoding/json"
    "golang.org/x/text/unicode/norm"
    "golang.org/x/text/transform"
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
    title     string
    class     string
    pid       int64
    process   Process
}
type Process struct {
    name    string
    cmdline string
}
type Event struct {
    eventType EventType
    window    Window
    time      uint
    timestamp int64
    code      uint8
}

type Counter struct {
    motions         uint
    filteredMotions uint
    clicks          uint
    scrolls         uint
    keys            uint
    time            uint
    start           time.Time
    end             time.Time
}

type Message struct {
    Event_type   string
    Xserver_time int64
    Timestamp    int64
    Wm_name      string
    Wm_class     string
    Pid          int64
    Proc_name    string
    Proc_cmd     string
    Code         uint8
}


var prevEvent Event
var counter Counter

// Map windows to ID in DB
var windows map[Window]int64
var procs map[Process]int64
var keys []Event

// DB
var db *sql.DB
var insertWindowCommand *sql.Stmt
var selectWindowCommand *sql.Stmt
var insertRecordCommand *sql.Stmt
var insertProcessCommand *sql.Stmt
var selectProcessCommand *sql.Stmt
var insertKeyCommand *sql.Stmt
var tx *sql.Tx

const idleTimeout = 300 * 1000 // milliseconds

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
    var m Message
    err := json.Unmarshal([]byte(parts[1]), &m)
    if err != nil {
        panic("parse error")
    }

    var eventType EventType
    
    switch m.Event_type {
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
    
    window := Window {
        title:     m.Wm_name,
        class:     m.Wm_class,
        pid:       m.Pid,
        process:   Process {
            name:    m.Proc_name,
            cmdline: m.Proc_cmd,
        },
    }

    return Event {
        eventType: eventType,
        window:    window,
        time:      uint(m.Xserver_time),
        timestamp: m.Timestamp,
        code:      m.Code,
    }
}

func processEvent(event Event) {
    defer func(){prevEvent = event}()
    var err error
    processWindow(event.window)
    if prevEvent.time == 0 {
        counter.start = time.Unix(event.timestamp, 0)
        return
    }

    delta := event.time - prevEvent.time
    counter.time += delta

    switch event.eventType {
    case MotionEvent:
        // filter mouse motions
        counter.motions += 1
        if prevEvent.eventType != MotionEvent || delta > 200 {
            counter.filteredMotions += 1
        }
    case ClickEvent:
        if event.code == 4 || event.code == 5 || event.code == 6 || event.code == 7 {
            counter.scrolls += 1
        } else {
            counter.clicks += 1
        }
    case KeyEvent:
        keys = append(keys, event)
        counter.keys += 1
    }

    if prevEvent.window != event.window || (event.time - prevEvent.time) > idleTimeout {
        if (event.time - prevEvent.time) > idleTimeout {
            counter.end = time.Unix(prevEvent.timestamp, 0)
        } else {
            counter.end = time.Unix(event.timestamp, 0)
        }
        windowId := windows[prevEvent.window]
        duration := counter.end.Sub(counter.start)
        // Start new transaction
        tx, err = db.Begin();
        if err != nil {
            panic("Could not start db transaction")
        }
        // store record
        tx.Stmt(insertRecordCommand).Exec(
            windowId,
            counter.start.Format(time.RFC3339Nano),
            counter.end.Format(time.RFC3339Nano),
            duration / time.Millisecond,
            counter.motions,
            counter.filteredMotions,
            counter.clicks,
            counter.scrolls,
            counter.keys,
            prevEvent.window.pid)
        // Store keys
        txInsertKeyCommand := tx.Stmt(insertKeyCommand)
        for i := 0; i < len(keys); i++ {
            t := time.Unix(keys[i].timestamp, 0)
            txInsertKeyCommand.Exec(windows[keys[i].window], keys[i].code, t.Format(time.RFC3339Nano))
        }
        // reset keys
        keys = make([]Event, 0)
        // reset counter
        counter = Counter {
            motions:         0,
            filteredMotions: 0,
            clicks:          0,
            scrolls:         0,
            keys:            0,
            time:            0,
            start:           time.Unix(event.timestamp, 0),
        }

        // Commit transaction
        tx.Commit()
    }
    
    // fmt.Printf("Window %s time %.3f sec motions: %d, clicks: %d, keys: %d\n", event.window.class, float32(counter.time)/1000, counter.motions, counter.clicks, counter.keys)
}

func processWindow(window Window) int64 {
    // Process proc_name
    if _, ok := procs[window.process]; !ok {
        // try to find procs in db
        var id int64
        var processName = window.process.name
        var cmdline = stripCtlAndExtFromUnicode(window.process.cmdline)
        err := selectProcessCommand.QueryRow(strings.TrimSpace(processName), strings.TrimSpace(cmdline)).Scan(&id)
        switch err {
        case sql.ErrNoRows:
            // store window to db
            res, _ := insertProcessCommand.Exec(strings.TrimSpace(processName), strings.TrimSpace(cmdline))
            id, _ = res.LastInsertId()
            procs[window.process] = id
        default:
            procs[window.process] = id
        }
    }
    proc_id := procs[window.process]

    // Process window
    if _, ok := windows[window]; !ok {
        // try to find window in db
        var id int64
        err := selectWindowCommand.QueryRow(strings.TrimSpace(window.title), window.class, proc_id).Scan(&id)
        switch err {
        case sql.ErrNoRows:
            // store window to db
            res, _ := insertWindowCommand.Exec(strings.TrimSpace(window.title), window.class, proc_id)
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
    procs   = make(map[Process]int64)
    keys    = make([]Event, 0)
    var err error

    // Determine path to db
    homePath, err := homedir.Dir()
    if err != nil {
        panic("Could not get user's HOME path")
    }
    homePath, err = homedir.Expand(homePath)
    if err != nil {
        panic("Could not exepnd '~' in user's HOME path")
    }

    // Create .selftop directory
    err = os.Mkdir(homePath + "/.selftop", 0775);
    if err != nil && !os.IsExist(err) {
        panic("Error: " + err.Error())
    }

    dbPath := homePath + "/.selftop/selftop.db"
    fmt.Printf("DB path: %s\n", dbPath)

    
    // Init db connection
    db, err = sql.Open("sqlite3", dbPath)
    if err != nil {
        panic("Could not create db connection")
    }
    
    initDbSchema()

    insertWindowCommand, err = db.Prepare(
        "INSERT INTO window (title, class, process_id) VALUES (?,?, ?)")
    if err != nil {
        panic("Could not create prepared statement." + err.Error())
    }

    selectWindowCommand, err = db.Prepare(
        "SELECT id FROM window WHERE title = ? AND class = ? AND process_id = ? LIMIT 1")
    if err != nil {
        panic("Could not create prepared statement." + err.Error())
    }

    selectProcessCommand, err = db.Prepare(
        "SELECT id FROM process WHERE name = ? AND cmdline = ? LIMIT 1")
    if err != nil {
        panic("Could not create prepared statement." + err.Error())
    }

    insertProcessCommand, err = db.Prepare(
        "INSERT INTO process (name, cmdline) VALUES (?, ?)")
    if err != nil {
        panic("Could not create prepared statement." + err.Error())
    }


    insertRecordCommand, err = db.Prepare(
        "INSERT INTO record (window_id, start, end, duration, motions, motions_filtered, clicks, scrolls, keys, pid) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)")
    if err != nil {
        panic("Could not create prepared statement." + err.Error())
    }

    insertKeyCommand, err = db.Prepare(
        "INSERT INTO keys (window_id, key, at) VALUES (?, ?, ?)")
    if err != nil {
        panic("Could not create prepared statement." + err.Error())
    }
}

func initDbSchema() {
    sql := `
    CREATE TABLE IF NOT EXISTS window (
        id         INTEGER PRIMARY KEY AUTOINCREMENT,
        process_id INTEGER,
        title      TEXT,
        class      TEXT,
        created    DATETIME DEFAULT CURRENT_TIMESTAMP
    );

    CREATE TABLE IF NOT EXISTS process (
        id      INTEGER PRIMARY KEY AUTOINCREMENT,
        name    TEXT,
        cmdline TEXT,
        alias   TEXT,
        created DATETIME DEFAULT CURRENT_TIMESTAMP,
        CONSTRAINT name_cmd UNIQUE (name, cmdline)
    );

    CREATE TABLE IF NOT EXISTS record (
        id               INTEGER PRIMARY KEY AUTOINCREMENT,
        pid              INTEGER NOT NULL,
        window_id        INTEGER NOT NULL,
        start            DATETIME NOT NULL,
        end              DATETIME NOT NULL,
        duration         INTEGER NOT NULL DEFAULT 0,
        motions          INTEGER NOT NULL DEFAULT 0,
        motions_filtered INTEGER NOT NULL DEFAULT 0,
        clicks           INTEGER NOT NULL DEFAULT 0,
        scrolls          INTEGER NOT NULL DEFAULT 0,
        keys             INTEGER NOT NULL DEFAULT 0,
        created          DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    
    CREATE TABLE IF NOT EXISTS keys (
        id          INTEGER PRIMARY KEY AUTOINCREMENT,
        window_id   INTEGER NOT NULL,
        key         INTEGER DEFAULT 0,
        at          DATETIME NOT NULL,
        created     DATETIME DEFAULT CURRENT_TIMESTAMP
    );

    CREATE TABLE IF NOT EXISTS clicks (
        id               INTEGER PRIMARY KEY AUTOINCREMENT,
        window_id        INTEGER NOT NULL,
        button           INTEGER NOT NULL,
        motions          INTEGER DEFAULT 0,
        motions_filtered INTEGER DEFAULT 0,
        created          DATETIME DEFAULT CURRENT_TIMESTAMP
    );

`

    _, err := db.Exec(sql)
    if (err != nil) {
        panic("Could not init database schema. " + err.Error());
    }
}

func stripCtlAndExtFromUnicode(str string) string {
    isOk := func(r rune) bool {
        return r < 32
    }
    // The isOk filter is such that there is no need to chain to norm.NFC
    t := transform.Chain(norm.NFKD, transform.RemoveFunc(isOk))
    // This Transformer could also trivially be applied as an io.Reader
    // or io.Writer filter to automatically do such filtering when reading
    // or writing data anywhere.
    str, _, _ = transform.String(t, str)
    return str
}