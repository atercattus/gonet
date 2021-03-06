package gonetz

import (
	"fmt"
	"net"
	"runtime"
	"syscall"
	"unsafe"
)

const (
//SO_REUSEPORT = 15 // missing in stdlib
)

type (
	// ConnEvent - это callback на собыия на сокете (пок что только на чтение (OnClientRead))
	ConnEvent func(conn *TCPConn) bool

	// TCPServer реализует TPC сервер
	TCPServer struct {
		closed bool
		fd     int
		epoll  EPoll

		workerPool workerPool

		acceptAddr struct {
			syscall.RawSockaddrAny
			Ptr    uintptr
			Len    uint32
			LenPtr uintptr
		}

		clients map[int]*TCPConn
		rdEvent ConnEvent
		wrEvent ConnEvent
	}

	workerPool struct {
		fds           []int
		epolls        []EPoll
		nextWorkerIdx int
	}
)

var (
	// ErrWrongHost возвращается при некорректном имени хоста в качестве listen адреса
	ErrWrongHost = fmt.Errorf(`wrong host`)
	// ErrWrongPoolSize возвращается при попытке создать пустой пул воркеров
	ErrWrongPoolSize = fmt.Errorf(`wrong pool size`)
)

// NewServer создает новый сервер на указанном адресе и порту
func NewServer(host string, port uint) (srv *TCPServer, err error) {
	srv = &TCPServer{}

	if err = srv.newListenerIPv4(host, port); err != nil {
		return nil, err
	} else if err = srv.setupServerWorkers(1); err != nil {
		srv.Close()
		return nil, err
	}

	srv.setupAcceptAddr()

	srv.clients = make(map[int]*TCPConn)

	// Заглушка, чтобы не проверять на nil в основном цикле
	srv.rdEvent = func(conn *TCPConn) bool {
		return true
	}

	return srv, err
}

// OnClientRead заменяет обработчик получения новых данных по соединению
func (srv *TCPServer) OnClientRead(event ConnEvent) {
	srv.rdEvent = event
}

//func (srv *TCPServer) OnClientWrite(event ConnEvent) {
//	srv.wrEvent = event
//}

func (srv *TCPServer) setupAcceptAddr() {
	srv.acceptAddr.Ptr = uintptr(unsafe.Pointer(&srv.acceptAddr.RawSockaddrAny))
	srv.acceptAddr.Len = syscall.SizeofSockaddrAny
	srv.acceptAddr.LenPtr = uintptr(unsafe.Pointer(&srv.acceptAddr.Len))
}

func (srv *TCPServer) newListenerIPv4(listenAddr string, listenPort uint) (err error) {
	if listenAddr == `` {
		listenAddr = `0.0.0.0`
	}

	ip := net.ParseIP(listenAddr)
	if len(ip) == 0 {
		return ErrWrongHost
	}

	addr := syscall.SockaddrInet4{Port: int(listenPort)}
	copy(addr.Addr[:], ip.To4())

	serverFd := 0

	if serverFd, err = syscallWrappers.Socket(syscall.AF_INET, syscall.O_NONBLOCK|syscall.SOCK_STREAM, 0); err != nil {
		return err
	} else if err = syscallWrappers.SetNonblock(serverFd, true); err != nil {
		//} else if err = syscall.SetsockoptInt(serverFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		//} else if err = syscall.SetsockoptInt(serverFd, syscall.SOL_SOCKET, SO_REUSEPORT, 1); err != nil {
	} else if err = syscallWrappers.SetsockoptInt(serverFd, syscall.SOL_TCP, syscall.TCP_NODELAY, 1); err != nil { // ?
	} else if err = syscallWrappers.SetsockoptInt(serverFd, syscall.SOL_TCP, syscall.TCP_QUICKACK, 1); err != nil {
	} else if err = syscallWrappers.Bind(serverFd, &addr); err != nil {
	} else if err = syscallWrappers.Listen(serverFd, maxEpollEvents); err != nil {
	} else if err = InitServerEpoll(serverFd, &srv.epoll); err != nil {
	} else {
		srv.fd = serverFd
		return nil
	}

	// something went wrong
	_ = syscall.Close(serverFd)

	return err
}

func (srv *TCPServer) setupServerWorkers(poolSize uint) (err error) {
	if poolSize < 1 {
		return ErrWrongPoolSize
	}

	pool := &srv.workerPool

	pool.fds = make([]int, poolSize)
	pool.epolls = make([]EPoll, poolSize)

	for i := 0; i < int(poolSize); i++ {
		epoll := &pool.epolls[i]

		if err = InitClientEpoll(epoll); err != nil {
			return err
		}
		pool.fds[i] = epoll.fd

		go func() {
			err := srv.startWorkerLoop(epoll)
			if err != nil {
				// ToDo: log
			}
		}()
	}

	return nil
}

// Start блокирующе запускает обработку новых соединений
func (srv *TCPServer) Start() error {
loop:
	for !srv.closed {
		_, errno := srv.epoll.Wait()
		if errno != 0 {
			if errno == syscall.EINTR {
				runtime.Gosched()
				continue
			}
			return errno
		}

		for {
			clientFd, errno := srv.accept()
			if errno != 0 {
				if errno == syscall.EAGAIN {
					// обработаны все новые коннекты
					continue loop
				}
				// ToDo: log
				continue
			}

			workerEpoll := srv.getWorkerEPoll()
			if err := workerEpoll.AddClient(clientFd); err != nil {
				_, _, _ = syscall.Syscall(syscall.SYS_CLOSE, uintptr(clientFd), 0, 0)
			} else {
				var conn TCPConn
				conn.fd = clientFd
				srv.clients[clientFd] = &conn
			}
		}
	}

	return nil
}

func (srv *TCPServer) getWorkerEPoll() *EPoll {
	pool := &srv.workerPool
	idx := pool.nextWorkerIdx
	pool.nextWorkerIdx = (pool.nextWorkerIdx + 1) % len(pool.fds)

	return &pool.epolls[idx]
}

func (srv *TCPServer) startWorkerLoop(epoll *EPoll) error {
	var (
		readBuf    = make([]byte, 32*1024)
		readBufPtr = uintptr(unsafe.Pointer(&readBuf[0]))
		readBufLen = uintptr(len(readBuf))
	)

	for {
		nEvents, errno := epoll.Wait()
		if errno != 0 {
			if errno == syscall.EINTR {
				runtime.Gosched()
				continue
			}
			return errno
		} else if nEvents == 0 {
			runtime.Gosched()
			continue
		}

		for ev := 0; ev < nEvents; ev++ {
			clientFd := int(epoll.events[ev].Fd)
			eventsMask := epoll.events[ev].Events

			if (eventsMask & syscall.EPOLLIN) != 0 {
				r1, _, errno := syscall.Syscall(syscall.SYS_READ, uintptr(clientFd), readBufPtr, readBufLen)
				nbytes := int(r1)

				if errno != 0 {
					if errno != syscall.EAGAIN { // если ошибка не про "обработаны все новые данные"
						// syscall.EBADF, syscall.ECONNRESET, ...
						srv.closeClient(epoll, clientFd)
					}
				} else if nbytes > 0 {
					if conn, ok := srv.clients[clientFd]; ok {
						_, _ = conn.RdBuf.Write(readBuf[:nbytes])
						srv.rdEvent(conn)
					}
				} else {
					// соединение закрылось
					srv.closeClient(epoll, clientFd)
				}
				//} else if (eventsMask & syscall.EPOLLOUT) != 0 {
				// можно записывать (если не получилось сразу весь ответ выслать)
				// }
			} else if (eventsMask & (syscall.EPOLLERR | syscall.EPOLLHUP)) != 0 {
				srv.closeClient(epoll, clientFd)
			}
		}
	}
}

func (srv *TCPServer) closeClient(clientEpoll *EPoll, clientFd int) {
	conn, ok := srv.clients[clientFd]
	if ok {
		conn.RdBuf.Clean()
		conn.WrBuf.Clean()
		delete(srv.clients, clientFd)
	}

	_ = clientEpoll.DeleteFd(clientFd)
	_, _, _ = syscall.Syscall(syscall.SYS_CLOSE, uintptr(clientFd), 0, 0)
}

// Close останавливает сервер
func (srv *TCPServer) Close() {
	srv.closed = true
	srv.closeClient(&srv.epoll, srv.fd)
	srv.fd = 0

	for _, epoll := range srv.workerPool.epolls {
		_, _, _ = syscall.Syscall(syscall.SYS_CLOSE, uintptr(epoll.fd), 0, 0)
	}
	srv.workerPool.epolls = srv.workerPool.epolls[:0]
}

func (srv *TCPServer) accept() (clientFd int, errno syscall.Errno) {
	r1, _, errno := syscallWrappers.Syscall(
		syscall.SYS_ACCEPT,
		uintptr(srv.fd),
		srv.acceptAddr.Ptr,
		srv.acceptAddr.LenPtr,
	)

	clientFd = int(r1)
	return clientFd, errno
}
