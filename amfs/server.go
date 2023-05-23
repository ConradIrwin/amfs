package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"

	"github.com/ConradIrwin/amfs/cfg"
	"github.com/ConradIrwin/parallel"
	"github.com/go-git/go-billy/v5"
	nfs "github.com/willscott/go-nfs"
)

type handler struct {
	fs billy.Filesystem
}

type amfs struct {
}

func main() {
	ctx := context.Background()
	ctx, err := cfg.Load(ctx)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	listener, err := net.Listen("tcp", cfg.Listen(ctx))
	if err != nil {
		panic(err)
	}
	fmt.Println("amfs listening on", listener.Addr())

	syncListener, err := net.Listen("unix", cfg.UnixListen(ctx))
	if err != nil {
		panic(err)
	}
	fmt.Println("amfs listening on", cfg.UnixListen(ctx))

	parallel.Do(func(p *parallel.P) {
		p.OnPanic = func(panick any) bool {
			fmt.Println(panick)
			debug.PrintStack()
			shutdown(ctx, listener, syncListener)
			return true
		}
		for _, m := range cfg.Mounts(ctx) {
			p.Go(func() { mount(ctx, m) })
		}

		p.Go(func() { handleInterrupt(ctx, listener, syncListener) })

		fs := NewAMFS()

		p.Go(func() {
			if err := serveSync(ctx, syncListener, fs); err != nil {
				panic(err)
			}
		})

		if err := nfs.Serve(listener, &handler{fs: fs}); err != nil {
			panic(err)
		}
	})
}

func mount(ctx context.Context, m *cfg.Mount) {
	cmd := exec.Command("mount", "-v", "-t", "nfs", "-o", cfg.MountOptions(ctx), m.Source, m.Mountpoint)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		fmt.Println("failed to mount "+m.Mountpoint, err)
	}
}

func handleInterrupt(ctx context.Context, listeners ...net.Listener) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c
	shutdown(ctx, listeners...)
}

func shutdown(ctx context.Context, listeners ...net.Listener) {
	fmt.Println("SHUTDOWN")
	for _, m := range cfg.Mounts(ctx) {
		cmd := exec.Command("umount", m.Mountpoint)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Println("failed to umount "+m.Mountpoint, err)
		}
	}
	for _, l := range listeners {
		l.Close()
	}
}

// Mount backs Mount RPC Requests, allowing for access control policies.
func (h *handler) Mount(ctx context.Context, conn net.Conn, req nfs.MountRequest) (status nfs.MountStatus, hndl billy.Filesystem, auths []nfs.AuthFlavor) {
	fmt.Println("Mount", conn, req)
	status = nfs.MountStatusOk
	hndl = h.fs
	auths = []nfs.AuthFlavor{nfs.AuthFlavorNull}
	return
}

// Change provides an interface for updating file attributes.
func (h *handler) Change(fs billy.Filesystem) billy.Change {
	return h.fs.(billy.Change)
}

// FSStat provides information about a filesystem.
func (h *handler) FSStat(ctx context.Context, f billy.Filesystem, s *nfs.FSStat) error {
	fmt.Println("FSSTAT", f, s)
	return nil
}

// ToHandle handled by CachingHandler
func (h *handler) ToHandle(f billy.Filesystem, s []string) []byte {
	file, err := f.(*AMFS).getFileInfo(f.(*AMFS).Join(s...), None, 0)
	if err != nil {
		panic(err)
	}
	handle := []byte(".amfs/=" + file.amid)
	fmt.Println("ToHandle %#v", string(handle))
	return handle
}

// FromHandle handled by CachingHandler
func (h *handler) FromHandle(handle []byte) (billy.Filesystem, []string, error) {
	fmt.Printf("FromHandle: %#v %#v\n", handle, string(handle))
	if !bytes.HasPrefix(handle, []byte(".amfs/=")) {
		return nil, nil, fmt.Errorf("invalid file handle: " + string(handle))
	}
	return h.fs, h.fs.(*AMFS).Split(string(handle)), nil
}

// HandleLImit handled by cachingHandler
func (h *handler) HandleLimit() int {
	fmt.Println("HandleLimit")
	return 1024 * 1024
}
