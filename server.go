package main

import (
	"log"
	"net"
	"net/http"
	"reflect"
	"sync"
	"syscall"

	"github.com/gobwas/ws"
	"golang.org/x/sys/unix"
	"github.com/gobwas/ws/wsutil"
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

func handleWriterEvents()
{
	for{
		event := make([]unix.EpollEvent, 1)
		n, err := unix.EpollWait(epoller.fdWriter, events, -1)
		if err != nil {
			return nil, err
		}

		
		if event.Events & unix.EPOLLIN !=0 {
			brodcastMessage()
		}else {

		}
		epoller.WriterLock.RLock()
		epoller.WriterLock.RUnlock()
	}
}

func handleReaderEvents(){
	for{
		connections, err := epoller.WaitReader()
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
	epoller.clients[fd] = conn
	defer epoller.Readerlock.Unlock()
	if len(epoller.clients)%100 == 0 {
		log.Printf("Total number of connections: %v", len(epoller.clients))
	}
	log.Println("Reader connection Succeed", epoller.sender)
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

func brodcastMessage(){
	msg, op, err := wsutil.ReadClientData()
		if err != nil {
			log.Println("Read error:", err)
			return
		}

		// Ensure the message is a text message
		if op == ws.OpText {
			var message Message
			// Unmarshal the JSON data into a Go struct
			err = json.Unmarshal(msg, &message)
			if err != nil {
				log.Println("Unmarshal error:", err)
				continue
			}

			// Print the received message
			fmt.Printf("Received message: %+v\n", message)
		} else {
			log.Println("Unsupported message type:", op)
		}
}