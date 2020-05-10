package gonet

import (
	"syscall"
	"unsafe"
)

const (
	maxEpollEvents = 2048
	EPOLLET        = 1 << 31 // У syscall.EPOLLET неудобный тип
)

type (
	Millisecond int

	EPoll struct {
		fd          int
		serverEvent syscall.EpollEvent

		eventsCap      int
		events         []syscall.EpollEvent
		eventsFirstPtr uintptr

		WaitTimeout Millisecond
	}
)

var (
	DefaultEPollWaitTimeout = Millisecond(10)
)

func InitClientEpoll(epoll *EPoll) (err error) {
	epoll.fd, err = syscallWrappers.EpollCreate1(0)
	if err != nil {
		return err
	}

	epoll.WaitTimeout = DefaultEPollWaitTimeout

	epoll.eventsCap = maxEpollEvents
	epoll.events = make([]syscall.EpollEvent, maxEpollEvents)
	epoll.eventsFirstPtr = uintptr(unsafe.Pointer(&epoll.events[0]))

	return nil
}

func InitServerEpoll(serverFd int, epoll *EPoll) (err error) {
	if err = InitClientEpoll(epoll); err != nil {
		return err
	}

	epoll.serverEvent.Events = syscall.EPOLLIN | EPOLLET
	epoll.serverEvent.Fd = int32(serverFd)

	if err = syscall.EpollCtl(epoll.fd, syscall.EPOLL_CTL_ADD, serverFd, &epoll.serverEvent); err != nil {
		_ = syscall.Close(epoll.fd)
		epoll.fd = 0
		return err
	}

	return nil
}

func (epoll *EPoll) DeleteFd(fd int) (err error) {
	return syscall.EpollCtl(epoll.fd, syscall.EPOLL_CTL_DEL, fd, nil)
}

func (epoll *EPoll) AddClient(clientFd int) (err error) {
	epoll.serverEvent.Events = syscall.EPOLLIN | EPOLLET // | syscall.EPOLLOUT
	epoll.serverEvent.Fd = int32(clientFd)

	if err = syscallWrappers.SetNonblock(clientFd, true); err != nil {
	} else if err = syscall.EpollCtl(epoll.fd, syscall.EPOLL_CTL_ADD, clientFd, &epoll.serverEvent); err != nil {
	} else if err = syscallWrappers.SetsockoptInt(clientFd, syscall.SOL_TCP, syscall.TCP_NODELAY, 1); err != nil {
	} else if err = syscallWrappers.SetsockoptInt(clientFd, syscall.SOL_TCP, syscall.TCP_QUICKACK, 1); err != nil {
	} else {
		return nil
	}

	return err
}

func (epoll *EPoll) Wait() (nEvents int, errno syscall.Errno) {
	r1, _, errno := syscallWrappers.Syscall6(
		syscall.SYS_EPOLL_WAIT,
		uintptr(epoll.fd),
		epoll.eventsFirstPtr,
		uintptr(epoll.eventsCap),
		uintptr(epoll.WaitTimeout),
		0,
		0,
	)
	return int(r1), errno
}
