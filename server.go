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
	clients		map[int]net.Conn
	sender      *net.Conn
	fdReader    int
	fdWriter	int
	Readerlock  *sync.RWMutex
	WriterLock  *sync.RWMutex
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


	go handleReaderEvents();
	go handleWriterEvents();

	http.HandleFunc("/sender", wsHandlerWriter)
	http.HandleFunc("/receiver", wsHandlerReader)
	log.Println("Server running in port 8000")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
}

func handleWriterEvents(){
	for{
		event := make([]unix.EpollEvent, 10)
		_, err := unix.EpollWait(epoller.fdWriter, event, -1)
		if err != nil {
			log.Println("Error getting file descriptor")
			return
		}
		if len(event)==0{
			log.Println("Hello")
			continue
		}
		log.Println("Got request for Closing Writer Connection")
		
		if event[0].Events & unix.EPOLLIN !=0 {
			log.Printf("Got Message from Writer Client. \n Broadcasting it. \n")
			msg, op, err := wsutil.ReadClientData(*epoller.sender)
			if err != nil {
				log.Println("Error Reading message from Writer client")
				continue
			}

			// Check if it's a text message (JSON is typically sent as text)
			if op != ws.OpText {
				log.Printf("Expected text message, got %d", op)
				continue
			}

			// Parse the JSON
			var message Message
			err = json.Unmarshal(msg, &message)
			if err != nil {
				log.Println("Error parsing JSON message")
			}
			brodcastMessage(message, op)
		}else {
			epoller.WriterLock.RLock()
			var temp net.Conn
			epoller.sender = &temp
			epoller.WriterLock.RLock()
		}
	}
}

func handleReaderEvents(){
	for{
		connections, err := epoller.WaitReader()

		if len(connections) == 0{
			continue
		}
		if err != nil {
			log.Printf("Failed to epoll wait %v", err)
			continue
		}
		for _, conn := range connections {
			if conn == nil {
				break
			}
			if err := epoller.RemoveReader(conn); err != nil {
				log.Printf("Failed to remove %v", err)
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
	e.Readerlock.Lock()
	defer e.Readerlock.Unlock()
	delete(e.clients, fd)
	if len(e.clients) % 100 == 0 {
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

func createEpoll() (*epoll, error){
	log.Println("Creating epoll..")
	fdReader, err := unix.EpollCreate1(0)
	if err != nil {
		log.Println("Error creating  Reader Epoll");
		return nil, err
	}

	fdWriter, err := unix.EpollCreate1(0)
	if err != nil{
		log.Println("Error creating Writer Epoll");
		return nil, err
	}

	log.Println("Epoll created")
	return &epoll{
		clients:     make(map[int]net.Conn),
		fdReader:    fdReader,
		// sender:      make(chan *net.Conn),
		fdWriter:    fdWriter,
		// unregister:  make(chan *int),
		// broadcast:   make(chan *data),
		// register:    make(chan *int),
		Readerlock:  &sync.RWMutex{},
		WriterLock:  &sync.RWMutex{},
	}, nil
}

func wsHandlerWriter(w http.ResponseWriter, r *http.Request){
	log.Println("Trying Connecting to Writer");
	log.Println(epoller.sender);

	if epoller.sender!=nil{
		http.Error(w, "Sender already connected", http.StatusConflict)
		log.Println("Received duplicate request for write")
		return
	}

	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Println("Error Connecting to Writer Client")
		return
	}

	log.Println("Connection Upgraded", conn)
	log.Println("Creating Events for writer in Epoll")

	fd := websocketFD(conn)
	err = unix.EpollCtl(epoller.fdWriter, syscall.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLERR | unix.EPOLLRDHUP , Fd: int32(fd)})
	if err != nil {
		log.Println("Error Occured creating epoll event")
		return 
	}
	
	epoller.WriterLock.Lock();
	defer epoller.WriterLock.Unlock()
	epoller.sender = &conn
	log.Println("Writer connection Succeed", epoller.sender)
}

func wsHandlerReader(w http.ResponseWriter, r *http.Request){
	log.Println("Trying Connecting to Reader")
	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Println("Error Connecting to Reader Client")
		return
	}
	log.Println("Connection Upgraded", conn)
	log.Println("Creating Events for writer in Epoll")

	fd := websocketFD(conn) 
	err = unix.EpollCtl(epoller.fdWriter, syscall.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Events: unix.EPOLLERR | unix.EPOLLRDHUP, Fd: int32(fd)})
	if err != nil {
		log.Println("Error Occured creating epoll event")
		return 
	}

	epoller.Readerlock.Lock()
	defer epoller.Readerlock.Unlock()
	epoller.clients[fd] = conn
	if len(epoller.clients)%100 == 0 {
		log.Printf("Total number of connections: %v", len(epoller.clients))
	}
	log.Println("Reader connection Succeed", conn)
}

func (epoller *epoll) WaitReader() ([]net.Conn, error) {
	events := make([]unix.EpollEvent, 100)
	n, err := unix.EpollWait(epoller.fdReader, events, 100)
	if err != nil {
		return nil, err
	}

	epoller.Readerlock.RLock()
	defer epoller.Readerlock.RUnlock()
	var connections []net.Conn
	for i := 0; i < n; i++ {
		conn := epoller.clients[int(events[i].Fd)]
		connections = append(connections, conn)
	}
	return connections, nil
}

func brodcastMessage(mes Message, op ws.OpCode){
	epoller.Readerlock.Lock()
	defer epoller.Readerlock.Lock()

	for _, ReaderClient := range epoller.clients {
		err := wsutil.WriteServerMessage(ReaderClient, op, []byte(mes.Content))
            if err != nil {
                log.Println("Write error:", err)
            }
	}
}