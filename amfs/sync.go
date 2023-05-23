package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/ConradIrwin/parallel"
	"github.com/automerge/automerge-go"
)

func serveSync(ctx context.Context, l net.Listener, fs *AMFS) error {
	parallel.Do(func(p *parallel.P) {
		p.OnPanic = func(pnk any) bool {
			fmt.Println("PANIC", pnk)
			debug.PrintStack()
			l.Close()
			return true
		}

		for {
			c, err := l.Accept()
			if err != nil {
				panic(err)
			}

			p.Go(func() {
				if err := serveConn(ctx, c, fs); err != nil {
					fmt.Println("error serving: ", err)
					c.Close()
				}
			})
		}
	})
	return nil
}

func serveConn(ctx context.Context, c net.Conn, fs *AMFS) error {
	rw := bufio.NewReadWriter(bufio.NewReader(c), bufio.NewWriter(c))

	syncers := map[AMID]*automerge.SyncState{}

	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimSuffix(line, "\n")
		cmd, tail, _ := strings.Cut(line, " ")

		switch cmd {
		case "PING":
			rw.WriteString("PONG " + tail + "\n")
		case "OPEN":
			i, err := fs.getFileInfo(tail, None, 0)
			if err != nil {
				rw.WriteString("ERROR " + line + ":" + err.Error() + "\n")
			} else if i.IsDir() {
				rw.WriteString("ERROR " + line + ": is directory\n")
			} else {
				if syncers[i.amid] == nil {
					if i.file.Type == Blob {
						content, err := os.ReadFile("fs/" + hex.EncodeToString(i.file.Heads[0]))
						if err != nil {
							fmt.Println("ERROR", err)
						}

						doc := automerge.New()
						if err := Tx(doc).
							Set("type").To("text").
							Set("content").To(automerge.NewText(string(content))).
							CommitOnly(); err != nil {
							panic(err)
						}
						syncers[i.amid] = automerge.NewSyncState(doc)
					} else {
						saved, err := os.ReadFile("fs/" + string(i.amid))
						if err != nil {
							panic(err)
						}
						doc, err := automerge.Load(saved)
						if err != nil {
							panic(err)
						}
						syncers[i.amid] = automerge.NewSyncState(doc)
					}
				}
				bytes := syncers[i.amid].Doc.Save()
				rw.WriteString("OPENED " + string(i.amid) + " " + fmt.Sprint(len(bytes)) + "\n")
				rw.Write(bytes)
				rw.WriteString("\n")
			}
		case "CLOSE":
			delete(syncers, AMID(tail))
			rw.WriteString("CLOSED " + tail + "\n")
		case "SYNC":
			id, size, _ := strings.Cut(tail, " ")

			l, err := strconv.Atoi(size)
			if err != nil || l > 1024*1024 {
				rw.WriteString("ERROR " + line + ": invalid size")
			}

			syncer, ok := syncers[AMID(id)]
			if !ok {
				rw.WriteString("ERROR " + line + ": not syncing")
			}

			if l > 0 {
				buf := make([]byte, l)
				if _, err := io.ReadFull(rw, buf); err != nil {
					return err
				}

				if err := syncer.ReceiveMessage(buf); err != nil {
					rw.WriteString("ERROR " + line + ": " + err.Error())
				}
				val, err := automerge.As[string](syncer.Doc.Path("content").Get())
				fmt.Printf("Document is now: %#v :: %#v", val, err)

				if err := os.WriteFile("fs/"+id, syncer.Doc.Save(), 0o644); err != nil {
					fmt.Printf("ERROR: ", err)
				}

				err = Tx(fs.doc).
					Set("files", id, "type").To(Mergeable).
					Set("files", id, "modtime").To(time.Now()).
					Set("files", id, "size").To(len(val)).
					Inc("files", id, "modcount").
					Set("files", id, "heads", 0).To(syncer.Doc.Heads()[0]).
					Commit()

				if err != nil {
					panic(err)
				}

			}

			msg, _ := syncer.GenerateMessage()
			rw.WriteString("SYNC " + id + " " + fmt.Sprint(len(msg)) + "\n")
			rw.Write(msg)
			rw.WriteString("\n")

		case "":
			// ignore empty lines
		default:
			rw.WriteString("ERROR " + line + ": unknown command\n")
		}

		if err := rw.Flush(); err != nil {
			return err
		}
	}
}
