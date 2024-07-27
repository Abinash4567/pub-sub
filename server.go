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
)

var epoller *epoll

type data struct {
	time int
	price int
}

type epoll struct {
	clients		map[int]net.Conn
	sender      chan *net.Conn
	fdReader    int
	fdWriter	int
	unregister  chan *int
	register	chan *int
	broadcast   chan *data
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


	http.HandleFunc("/sender", wsHandlerWriter)
	log.Println("Server running in port 8000")
	if err := http.ListenAndServe(":8000", nil); err != nil {
		log.Fatal(err)
	}
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

	// if epoller.sender!=nil{
	// 		http.Error(w, "Sender already connected", http.StatusConflict)
	// 		return
	// 	}


	conn, _, _, err := ws.UpgradeHTTP(r, w)
	if err != nil {
		log.Println("Error Connecting to Writer Client")
		return
	}

	log.Println("Connection Upgraded", conn)
	log.Println("Creating Events for writer in Epoll")

	fd := websocketFD(conn) 
	err = unix.EpollCtl(epoller.fdWriter, syscall.EPOLL_CTL_ADD, fd, &unix.EpollEvent{Events: unix.EPOLLIN | unix.EPOLLERR | unix.EPOLLRDHUP | unix.EPOLLHUP , Fd: int32(fd)})
	if err != nil {
		log.Println("Error Occured creating epoll event")
		return 
	}
	
	epoller.sender <- &conn
	log.Println("Writer connection Succeed", epoller.sender)
}