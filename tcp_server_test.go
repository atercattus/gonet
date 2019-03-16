package gonet

import (
	"net"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

func getSocketPort(fd int) int {
	if sa, err := syscall.Getsockname(fd); err != nil {
		return 0
	} else if sa4, ok := sa.(*syscall.SockaddrInet4); ok {
		return sa4.Port
	} else if sa6, ok := sa.(*syscall.SockaddrInet6); ok {
		return sa6.Port
	} else {
		return 0
	}
}

func Test_TCP_setupAcceptAddr(t *testing.T) {
	var srv TCPServer

	srv.setupAcceptAddr()
	if srv.acceptAddrPtr == 0 {
		t.Fatalf(`srv.acceptAddrPtr == 0`)
	}

	if srv.acceptAddrLen == 0 {
		t.Fatalf(`srv.acceptAddrLen == 0`)
	}

	if srv.acceptAddrLenPtr == 0 {
		t.Fatalf(`srv.acceptAddrLenPtr == 0`)
	}
}

func Test_TCP_makeListener_1(t *testing.T) {
	var srv TCPServer

	if err := srv.makeListener(`lol.kek`, 0); err == nil {
		t.Fatalf(`makeListener for wrong host was successfull`)
	} else if err != ErrWrongHost {
		t.Fatalf(`makeListener for wrong host returned wrong error`)
	}

	if err := srv.makeListener(``, 0); err != nil {
		t.Fatalf(`makeListener with empty host failed: %s`, err)
	}

	if err := srv.makeListener(`127.0.0.1`, 0); err != nil {
		t.Fatalf(`makeListener with 127.0.0.1 host failed: %s`, err)
	}
}

func Test_TCP_makeListener_2(t *testing.T) {
	var srv TCPServer

	SyscallWrappers.setWrongSocket()
	err := srv.makeListener(``, 0)
	SyscallWrappers.setRealSocket()
	if err == nil {
		t.Fatalf(`makeListener with wrong syscall.Socket was successfull`)
	}

	SyscallWrappers.setWrongSetNonblock()
	err = srv.makeListener(``, 0)
	SyscallWrappers.setRealSetNonblock()
	if err == nil {
		t.Fatalf(`makeListener with wrong syscall.SetNonblock was successfull`)
	}

	SyscallWrappers.setWrongSetsockoptInt(nil)
	err = srv.makeListener(``, 0)
	SyscallWrappers.setRealSetsockoptInt()
	if err == nil {
		t.Fatalf(`makeListener with wrong syscall.SetsockoptInt was successfull`)
	}

	SyscallWrappers.setWrongBind()
	err = srv.makeListener(``, 0)
	SyscallWrappers.setRealBind()
	if err == nil {
		t.Fatalf(`makeListener with wrong syscall.Bind was successfull`)
	}

	SyscallWrappers.setWrongListen()
	err = srv.makeListener(``, 0)
	SyscallWrappers.setRealListen()
	if err == nil {
		t.Fatalf(`makeListener with wrong syscall.Listen was successfull`)
	}
}

func Test_TCP_setupServerWorkers_1(t *testing.T) {
	var srv TCPServer

	if err := srv.setupServerWorkers(0); err == nil {
		t.Fatalf(`setupServerWorkers successed with 0 pool size`)
	}

	const (
		poolSize         = 1
		epollWaitTimeout = 10
	)
	DefaultEPollWaitTimeout = epollWaitTimeout // для проверки srv.workerPool ниже

	if err := srv.setupServerWorkers(poolSize); err != nil {
		t.Fatalf(`setupServerWorkers failed: %s`, err)
	}

	if l := len(srv.workerPool.epolls); l != poolSize {
		t.Fatalf(`pool size after setupServerWorkers is wrong: expect %d got %d`, poolSize, l)
	}

	if l := len(srv.workerPool.fds); l != poolSize {
		t.Fatalf(`fds size after setupServerWorkers is wrong: expect %d got %d`, poolSize, l)
	}

	fds := map[int]struct{}{}

	for _, fd := range srv.workerPool.fds {
		if fd == 0 {
			t.Fatalf(`fd == 0 in pool`)
		} else if _, ok := fds[fd]; ok {
			t.Fatalf(`same fd for different worker pools`)
		} else {
			fds[fd] = struct{}{}
		}
	}

	for _, epoll := range srv.workerPool.epolls {
		if epoll.fd == 0 {
			t.Fatalf(`worker epoll is not initialized`)
		}
	}

	// test event loop (primitive)
	clientFd, err := syscall.Socket(syscall.AF_INET, syscall.O_NONBLOCK|syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf(`cannot create test socket: %s`, err)
	}

	srv.workerPool.epolls[0].AddClient(clientFd)
	time.Sleep(10 * epollWaitTimeout * time.Millisecond)
	syscall.Syscall(syscall.SYS_CLOSE, uintptr(clientFd), 0, 0)
	time.Sleep(10 * epollWaitTimeout * time.Millisecond)

	srv.Close()
}

func Test_TCP_setupServerWorkers_2(t *testing.T) {
	var srv TCPServer

	SyscallWrappers.setWrongEpollCreate1(nil)
	err := srv.setupServerWorkers(1)
	SyscallWrappers.setRealEpollCreate1()
	if err == nil {
		t.Fatalf(`setupServerWorkers with wrong syscall.EpollCreate1 was successfull`)
	}

	SyscallWrappers.setWrongEpollCreate1(
		CheckFuncSkipN(1, nil),
	)
	err = srv.setupServerWorkers(1)
	SyscallWrappers.setRealEpollCreate1()
	if err == nil {
		t.Fatalf(`setupServerWorkers with wrong syscall.EpollCreate1 (skip 1) was successfull`)
	}
}

func Test_TCP_accept(t *testing.T) {
	var srv TCPServer

	if clientFd, errno := srv.accept(); clientFd != -1 || errno == 0 {
		t.Fatalf(`unexpected accept response for wrong call. clientFd:%d errno:%d`, clientFd, errno)
	}

	// ToDo:
}

func Test_TCP_close(t *testing.T) {
	// ToDo:
}

func Test_TCP_Close(t *testing.T) {
	// ToDo:
}

func Test_TCP_getWorkerEPoll(t *testing.T) {
	// ToDo:
}

func Test_TCP_MakeServer(t *testing.T) {
	if _, err := MakeServer(`lol.kek`, 0); err == nil {
		t.Fatalf(`MakeServer didnt failed with wrong listen addr`)
	}

	SyscallWrappers.setWrongEpollCreate1(
		CheckFuncSkipN(1, nil),
	)
	_, err := MakeServer(``, 0)
	SyscallWrappers.setRealEpollCreate1()
	if err == nil {
		t.Fatalf(`MakeServer didnt failed with wrong syscall.EpollCreate1`)
	}

	srv, err := MakeServer(`127.0.0.1`, 0)
	if err != nil {
		t.Fatalf(`MakeServer failed: %s`, err)
	}
	defer srv.Close()

	port := getSocketPort(srv.fd)
	if port == 0 {
		t.Fatalf(`Cannot determine test socket port`)
	}
}

func Test_TCP_Start_1(t *testing.T) {
	DefaultEPollWaitTimeout = 10

	SyscallWrappers.setWrongSyscall6(
		CheckFuncSyscallTrapSkipN(syscall.SYS_EPOLL_WAIT, 1, nil),
	)
	defer SyscallWrappers.setRealSyscall6()

	srv, err := MakeServer(`127.0.0.1`, 0)
	if err != nil {
		t.Fatalf(`MakeServer failed: %s`, err)
	}
	defer srv.Close()

	timeLimiter := time.After(1 * time.Second)
	success := make(chan bool, 1)

	go func() {
		success <- srv.Start() == nil
	}()

	select {
	case <-timeLimiter:
		success <- true
	case succ := <-success:
		if succ {
			t.Fatalf(`Successfull server start with wrong syscall.Syscall6`)
		}
	}
}

func Test_TCP_Start_2(t *testing.T) {
	srv, err := MakeServer(`127.0.0.1`, 0)
	if err != nil {
		t.Fatalf(`MakeServer failed: %s`, err)
	}
	defer srv.Close()

	port := getSocketPort(srv.fd)
	if port == 0 {
		t.Errorf(`Cannot determine test socket port`)
		return
	}

	timeLimiter := time.After(1 * time.Second)
	success := make(chan bool, 1)
	go func() {
		SyscallWrappers.setWrongSyscall(
			CheckFuncSyscallTrapSkipN(syscall.SYS_ACCEPT, 0, nil),
		)
		defer SyscallWrappers.setRealSyscall()

		go func() {
			succ := srv.Start() == nil
			success <- succ
		}()

		if client, err := net.Dial(`tcp`, `127.0.0.1:`+strconv.Itoa(port)); err != nil {
		} else {
			client.Write([]byte(`test you`))
			client.Close()
		}
	}()

	select {
	case <-timeLimiter:
		success <- false
		t.Errorf(`Timelimit when server try to start with wrong syscall.Syscall(syscall.SYS_ACCEPT)`)
		return
	case succ := <-success:
		if succ {
			t.Errorf(`Successfull server start with wrong syscall.Syscall(syscall.SYS_ACCEPT)`)
			return
		}
	}
}

func Test_TCP_Start_3(t *testing.T) {
	srv, err := MakeServer(`127.0.0.1`, 0)
	if err != nil {
		t.Fatalf(`MakeServer failed: %s`, err)
	}
	defer srv.Close()

	port := getSocketPort(srv.fd)
	if port == 0 {
		t.Fatalf(`Cannot determine test socket port`)
	}

	timeLimiter := time.After(1 * time.Second)
	success := make(chan bool, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		SyscallWrappers.setWrongSetsockoptInt(nil)
		defer SyscallWrappers.setRealSetsockoptInt()

		go func() {
			succ := srv.Start() == nil
			success <- succ
		}()

		if client, err := net.Dial(`tcp`, `127.0.0.1:`+strconv.Itoa(port)); err != nil {
		} else {
			client.Write([]byte(`test you`))
			client.Close()
		}
		wg.Done()
	}()

	wg.Wait()

	select {
	case <-timeLimiter:
		t.Fatalf(`Successfull server start with wrong syscall.SetsockoptInt`)
	case succ := <-success:
		if succ {
			t.Fatalf(`Successfull server start with wrong syscall.SetsockoptInt`)
		}
	}
}

func Test_TCP_startWorkerLoop(t *testing.T) {
	var srv TCPServer

	SyscallWrappers.setWrongSyscall6(
		CheckFuncSyscallTrapSkipN(syscall.SYS_EPOLL_WAIT, 0, nil),
	)
	srv.setupServerWorkers(1)
	err := srv.startWorkerLoop(&srv.workerPool.epolls[0])
	SyscallWrappers.setRealSyscall6()
	if err == nil {
		t.Fatalf(`setupServerWorkers with wrong syscall.Syscall6(syscall.SYS_EPOLL_WAIT) was successfull`)
	}
}