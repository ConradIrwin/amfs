import * as net from 'net'
import * as automerge from '@automerge/automerge'
import * as vscode from 'vscode'

type TextDoc = {
  type: string
  content: automerge.Text
}

export class Doc {
  doc: automerge.Doc<TextDoc>
  amid: string
  conn: Connection
  sync: automerge.SyncState

  constructor (conn: Connection, amid: string, doc: automerge.Doc<TextDoc>) {
    this.conn = conn
    this.amid = amid
    this.doc = doc
    this.sync = automerge.initSyncState()
  }

  async edit (
    changes: readonly {
      rangeOffset: number
      rangeLength: number
      text: string
    }[]
  ) {
    this.doc = automerge.change(this.doc, d => {
      for (let c of changes) {
        if (c.rangeLength > 0) {
          d.content.deleteAt(c.rangeOffset, c.rangeLength)
        }
        if (c.text.length > 0) {
          console.log('insert text: ', JSON.stringify(c.text))
          d.content.insertAt(c.rangeOffset, ...c.text.split(''))
        }
      }
    })

    await this.generate()
  }

  async receive (buf: Buffer) {
    ;[this.doc, this.sync] = automerge.receiveSyncMessage(
      this.doc,
      this.sync,
      buf
    )
    await this.generate()
  }

  async generate () {
    let [sync, msg] = automerge.generateSyncMessage(this.doc, this.sync)
    if (msg !== null) {
      let sock = await this.conn.connect()
      sock.write('SYNC ' + this.amid + ' ' + msg.length + '\n')
      sock.write(msg)
      sock.write('\n')
      this.sync = sync
    }
  }
}

export class Connection {
  connection: Promise<net.Socket> | undefined
  request:
    | {
        resolve: (a: any) => void
        reject: (e: Error) => void
      }
    | undefined
  buffer: Buffer
  docs: { [key: string]: Doc }

  constructor () {
    this.buffer = Buffer.alloc(0)
    this.docs = {}
  }

  async open (name: string): Promise<Doc> {
    let sock = await this.connect()
    sock.write('OPEN ' + name + '\n')
    return new Promise<{ amid: string; buf: Buffer }>((resolve, reject) => {
      this.request = { resolve, reject }
    }).then(({ amid, buf }) => {
      let doc = automerge.load<TextDoc>(buf)
      this.docs[amid] = new Doc(this, amid, doc)
      return this.docs[amid]
    })
  }

  async close (amid: string): Promise<void> {
    let sock = await this.connect()
    sock.write('CLOSE ' + amid + '\n')
    return new Promise<void>((resolve, reject) => {
      this.request = { resolve, reject }
    })
  }

  async connect () {
    this.connection ||= new Promise((resolve, reject) => {
      const client = net.createConnection('/tmp/amfs.sock')

      client.on('connect', () => resolve(client))
      client.on('data', data => this.receive(data))
      client.on('end', () => {
        delete this.connection
        if (this.request) {
          this.request.reject(new Error('connection closed'))
        }
      })
      client.on('error', error => {
        reject(error)
        if (this.request) {
          this.request.reject(error)
        }
      })
    })
    return this.connection
  }

  receive (data: Buffer) {
    this.buffer = Buffer.concat([this.buffer, data])

    while (this.buffer.length > 0) {
      let eol = this.buffer.indexOf('\n')
      if (eol === -1) return

      let line = this.buffer.toString('utf-8', 0, eol)
      this.buffer = this.buffer.slice(eol + 1)

      console.log('received ', line)

      let tokens = line.split(' ')
      switch (tokens[0]) {
        case 'PONG':
          if (this.request) {
            this.request.resolve(null)
          }
          break

        case 'CLOSED':
          if (this.request) {
            this.request.resolve(null)
          }
          break

        case 'OPENED':
          {
            let amid = tokens[1]
            let len = parseInt(tokens[2], 10)

            if (this.buffer.length < len) {
              return
            }
            let buf = this.buffer.slice(0, len)
            this.buffer = this.buffer.slice(len)

            if (this.request) {
              this.request.resolve({ amid, buf })
            }
          }
          break

        case 'SYNC':
          {
            let amid = tokens[1]
            let len = parseInt(tokens[2], 10)

            if (this.buffer.length < len) {
              return
            }
            let buf = this.buffer.slice(0, len)
            this.buffer = this.buffer.slice(len)

            if (this.docs[amid]) {
              this.docs[amid].receive(buf)
            }
          }
          break

        case '':
        // ignore empty lines
      }
    }
  }
}
