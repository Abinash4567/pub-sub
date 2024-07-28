package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"reflect"
	"sync"
	"syscall"

	"github.com/gobwas/ws"
	"github.com/gobwas/ws/wsutil"
	"golang.org/x/sys/unix"
)

var epoller *epoll

type epoll struct {
	clients    map[int]net.Conn
	sender     net.Conn
	fdReader   int
	fdWriter   int
	ReaderLock *sync.RWMutex
	WriterLock *sync.RWMutex
}

type Message struct {
	Type    string `json:"type"`
	Content string `json:"content"`
}

func main() {
	var err error
	epoller, err = createEpoll()
	if err != nil {
		log.Println("Error Creating epoll")
		panic(err)
	}
	log.Println(epoller)

	go handleReaderEvents()
	go handleWriterEvents()

	http.HandleFunc("/sender", wsHandlerWriter)
	http.HandleFunc("/receiver", wsHandlerReader)
	log.Println("Server running on port 8000")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}

func handleWriterEvents() {
    for {
        events := make([]unix.EpollEvent, 10) // Increase buffer size
        n, err := unix.EpollWait(epoller.fdWriter, events, -1)
        if err != nil {
            if err == syscall.EINTR {
                continue // Interrupted system call, just retry
            }
            log.Println("Error in EpollWait:", err)
            continue // Continue instead of returning
        }

        for i := 0; i < n; i++ {
            event := events[i]
            log.Println("Got request for Writer Connection:", event)

            epoller.WriterLock.Lock() // Lock for all operations

            if event.Events&unix.EPOLLIN != 0 {
                if epoller.sender == nil {
                    log.Println("Sender is nil")
                    epoller.WriterLock.Unlock()
                    continue
                }

                log.Printf("Got Message from Writer Client. Broadcasting it.\n")
                msg, op, err := wsutil.ReadClientData(epoller.sender)
                if err != nil {
                    log.Println("Error Reading message from Writer client:", err)
                    closeWriterConnection()
                    epoller.WriterLock.Unlock()
                    continue
                }

                if op != ws.OpText {
                    log.Printf("Expected text message, got %d", op)
                    epoller.WriterLock.Unlock()
                    continue
                }

                var message Message
                err = json.Unmarshal(msg, &message)
                if err != nil {
                    log.Println("Error parsing JSON message:", err)
                    epoller.WriterLock.Unlock()
                    continue
                }

                epoller.WriterLock.Unlock()
                broadcastMessage(message, op)
            } else if event.Events&(unix.EPOLLERR|unix.EPOLLHUP) != 0 {
                closeWriterConnection()
                epoller.WriterLock.Unlock()
            } else {
                log.Printf("Unexpected event type: %v", event.Events)
                epoller.WriterLock.Unlock()
            }
        }
    }
}

func closeWriterConnection() {
    if epoller.sender != nil {
        fd := websocketFD(epoller.sender)
        err := unix.EpollCtl(epoller.fdWriter, syscall.EPOLL_CTL_DEL, fd, nil)
        if err != nil {
            log.Println("Error while removing Writer Client:", err)
        }
        epoller.sender.Close()
        epoller.sender = nil
    }
}

func handleReaderEvents() {
	for {
		connections, err := epoller.WaitReader()
		if len(connections) == 0 {
			continue
		}
		
		if err != nil {
			log.Printf("Failed to epoll wait: %v", err)
			continue
		}

		for _, conn := range connections {
			if conn == nil {
				break
			}
			if err := epoller.RemoveReader(conn); err != nil {
				log.Printf("Failed to remove: %v", err)
			}
			conn.Close()
			log.Println("Reader Removed")
		}
	}
}

func (e *epoll) RemoveReader(conn net.Conn) error {
	fd := websocketFD(conn)
	err := unix.EpollCtl(e.fdReader, syscall.EPOLL_CTL_DEL, fd, nil)
	if err != nil {
		return err
	}
	e.ReaderLock.Lock()
	defer e.ReaderLock.Unlock()
	delete(e.clients, fd)
	if len(e.clients)%100 == 0 {
		log.Printf("Total number of connections: %v", len(e.clients))
	}
	log.Println("Removed Reader Client")
	return nil
}

func websocketFD(conn net.Conn) int {
	tcpConn := reflect.Indirect(reflect.ValueOf(conn)).FieldByName("conn")
	fdVal := tcpConn.FieldByName("fd")
	pfdVal := reflect.Indirect(fdVal).FieldByName("pfd")

	return int(pfdVal.FieldByName("Sysfd").Int())
}

func createEpoll() (*epoll, error) {
	log.Println("Creating epoll..")
	fdReader, err := unix.EpollCreate1(0)
	if err != nil {
		log.Println("Error creating Reader Epoll:", err)
		return nil, err
	}

	fdWriter, err := unix.EpollCreate1(0)
	if err != nil {
		unix.Close(fdReader)
		log.Println("Error creating Writer Epoll:", err)
		return nil, err
	}

	log.Println("Epoll created")
	return &epoll{
		clients:    make(map[int]net.Conn),
		fdReader:   fdReader,
		fdWriter:   fdWriter,
		ReaderLock: &sync.RWMutex{},
		WriterLock: &sync.RWMutex{},
	}, nil
}

func wsHandlerWriter(w http.ResponseWriter, r *http.Request) {
	log.Println("Trying to connect to Writer")
	if epoller.sender != nil {
		http.Error(w, "Sender already connected", http.StatusConflict)
		log.Println("Received duplicate request for write")
		return
	}

	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Println("Error connecting to Writer Client:", err)
		return
	}

	log.Println("Connection Upgraded:", conn)
	fd := websocketFD(conn)
	err = unix.EpollCtl(epoller.fdWriter, syscall.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLERR | unix.EPOLLRDHUP, Fd: int32(fd)})
	if err != nil {
		log.Println("Error creating epoll event:", err)
		conn.Close()
		return
	}

	epoller.WriterLock.Lock()
	defer epoller.WriterLock.Unlock()
	epoller.sender = conn
	log.Println("Writer connection succeeded:", epoller.sender)
}

func wsHandlerReader(w http.ResponseWriter, r *http.Request) {
	log.Println("Trying to connect to Reader")
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Println("Error connecting to Reader Client:", err)
		return
	}

	log.Println("Connection Upgraded:", conn)
	fd := websocketFD(conn)
	err = unix.EpollCtl(epoller.fdReader, syscall.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLERR | unix.EPOLLRDHUP, Fd: int32(fd)})
	if err != nil {
		log.Println("Error creating epoll event:", err)
		conn.Close()
		return
	}

	epoller.ReaderLock.Lock()
	defer epoller.ReaderLock.Unlock()
	epoller.clients[fd] = conn
	if len(epoller.clients)%100 == 0 {
		log.Printf("Total number of connections: %v", len(epoller.clients))
	}
	log.Println("Reader connection succeeded:", conn)
}

func (epoller *epoll) WaitReader() ([]net.Conn, error) {
	events := make([]unix.EpollEvent, 100)
	n, err := unix.EpollWait(epoller.fdReader, events, 1)
	if err != nil {
		return nil, err
	}

	epoller.ReaderLock.RLock()
	defer epoller.ReaderLock.RUnlock()
	var connections []net.Conn
	for i := 0; i < n; i++ {
		conn := epoller.clients[int(events[i].Fd)]
		if events[i].Events&(unix.EPOLLERR|unix.EPOLLHUP|unix.EPOLLRDHUP) != 0 {
			connections = append(connections, conn)
		}
	}
	return connections, nil
}

func broadcastMessage(mes Message, op ws.OpCode) {
	epoller.ReaderLock.RLock()
	defer epoller.ReaderLock.RUnlock()

	for _, readerClient := range epoller.clients {
		err := wsutil.WriteServerMessage(readerClient, op, []byte(mes.Content))
		if err != nil {
			log.Println("Write error:", err)
		}
	}
}