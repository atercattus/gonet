package gonet

import (
	"fmt"
	"net"
	"syscall"
	"unsafe"
)

const (
//SO_REUSEPORT = 15 // missing in stdlib
)

type (
	TCPServer struct {
		fd    int
		epoll EPoll

		workerPool WorkerPool

		acceptAddr       syscall.RawSockaddrAny
		acceptAddrPtr    uintptr
		acceptAddrLen    uint32
		acceptAddrLenPtr uintptr
	}

	WorkerPool struct {
		fds           []int
		epolls        []EPoll
		nextWorkerIdx int
	}
)

var (
	ErrWrongHost     = fmt.Errorf(`wrong host`)
	ErrWrongPoolSize = fmt.Errorf(`wrong pool size`)
)

func MakeServer(host string, port uint) (srv *TCPServer, err error) {
	srv = &TCPServer{}

	if err = srv.makeListener(host, port); err != nil {
		return nil, err
	} else if err = srv.setupServerWorkers(1); err != nil {
		srv.Close()
		return nil, err
	}

	srv.setupAcceptAddr()

	return srv, err
}

func (srv *TCPServer) setupAcceptAddr() {
	srv.acceptAddrPtr = uintptr(unsafe.Pointer(&srv.acceptAddr))
	srv.acceptAddrLen = syscall.SizeofSockaddrAny
	srv.acceptAddrLenPtr = uintptr(unsafe.Pointer(&srv.acceptAddrLen))
}

func (srv *TCPServer) makeListener(listenAddr string, listenPort uint) (err error) {
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

	if serverFd, err = SyscallWrappers.Socket(syscall.AF_INET, syscall.O_NONBLOCK|syscall.SOCK_STREAM, 0); err != nil {
		return err
	} else if err = SyscallWrappers.SetNonblock(serverFd, true); err != nil {
		//} else if err = syscall.SetsockoptInt(serverFd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		//} else if err = syscall.SetsockoptInt(serverFd, syscall.SOL_SOCKET, SO_REUSEPORT, 1); err != nil {
	} else if err = SyscallWrappers.SetsockoptInt(serverFd, syscall.SOL_TCP, syscall.TCP_NODELAY, 1); err != nil { // ?
	} else if err = SyscallWrappers.SetsockoptInt(serverFd, syscall.SOL_TCP, syscall.TCP_QUICKACK, 1); err != nil {
	} else if err = SyscallWrappers.Bind(serverFd, &addr); err != nil {
	} else if err = SyscallWrappers.Listen(serverFd, maxEpollEvents); err != nil {
	} else if err = InitServerEpoll(serverFd, &srv.epoll); err != nil {
	} else {
		// all ok
		srv.fd = serverFd
		return nil
	}

	// something went wrong
	syscall.Close(serverFd)

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
		i := i

		epollFd, err := SyscallWrappers.EpollCreate1(0)
		if err != nil {
			return err // Каков шанс, что тут может возникнуть ошибка?
		}
		pool.fds[i] = epollFd

		epoll := &pool.epolls[i]

		if err = InitClientEpoll(epoll); err != nil {
			return err // Каков шанс, что тут может возникнуть ошибка?
		}

		go srv.startWorkerLoop(epoll)
	}

	return nil
}

func (srv *TCPServer) Start() error {
	var (
		epoll = srv.epoll
	)

	//runtime.LockOSThread()

loop:
	for {
		_, errno := epoll.Wait()
		if errno != 0 {
			if errno == syscall.EINTR {
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
				return errno
			}

			workerEpoll := srv.getWorkerEPoll()
			if err := workerEpoll.AddClient(clientFd); err != nil {
				syscall.Syscall(syscall.SYS_CLOSE, uintptr(clientFd), 0, 0)
			}
		}
	}
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

	//runtime.LockOSThread()

	for {
		nEvents, errno := epoll.Wait()

		if errno != 0 {
			if errno == syscall.EINTR {
				continue
			}
			return errno
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
						srv.close(epoll, clientFd)
					}
				} else if nbytes > 0 {
					//if uintptr(nbytes) == readBufLen {
					//	fmt.Println(`ERROR: Max buff read!`)
					//}

					// ToDo: ...
					fmt.Printf("%v (%s)\n", readBuf[:nbytes], readBuf[:nbytes])
				} else {
					// соединение закрылось
					srv.close(epoll, clientFd)
				}
				//} else if (eventsMask & syscall.EPOLLOUT) != 0 {
				// можно записывать (если не получилось сразу весь ответ выслать)
				// }
			} else if (eventsMask & (syscall.EPOLLERR | syscall.EPOLLHUP)) != 0 {
				srv.close(epoll, clientFd)
			}
		}
	}
}

func (srv *TCPServer) close(clientEpoll *EPoll, clientFd int) {
	// ToDo: стоит проверять ошибки :)
	clientEpoll.DeleteFd(clientFd)
	syscall.Syscall(syscall.SYS_CLOSE, uintptr(clientFd), 0, 0)
}

func (srv *TCPServer) Close() {
	srv.close(&srv.epoll, srv.fd)
	srv.fd = 0
}

func (srv *TCPServer) accept() (clientFd int, errno syscall.Errno) {
	r1, _, errno := SyscallWrappers.Syscall(syscall.SYS_ACCEPT, uintptr(srv.fd), srv.acceptAddrPtr, srv.acceptAddrLenPtr)
	clientFd = int(r1)
	return clientFd, errno
}