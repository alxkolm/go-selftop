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
    window Window
    time uint
}

func main() {
    var err error
    var msg []byte
	createSocket()
    for {
        if msg, err = sock.Recv(); err != nil {
            die("Cannot recv: %s", err.Error())
        }
        event := parseMessage(string(msg))
        fmt.Printf("%d %d %s\n", event.eventType, event.time, event.window.title)
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
        window: window,
        time: uint(time),
    }
}