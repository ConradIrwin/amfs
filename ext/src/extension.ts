// The module 'vscode' contains the VS Code extensibility API
// Import the module and reference it with the alias vscode in your code below
import * as vscode from 'vscode'
import * as automerge from '@automerge/automerge'
import * as fs from 'fs/promises'
import { open } from 'fs'
import * as net from 'net'
import * as sync from './sync'
import { text } from 'stream/consumers'

let docs: { [uri: string]: sync.Doc } = {}

let conn = new sync.Connection()

const openTextDoc = async (textDoc: vscode.TextDocument) => {
  console.log(
    'did open text document',
    textDoc.uri.scheme,
    textDoc.uri.toString(),
    textDoc.getText()
  )

  if (textDoc.uri.fsPath.startsWith('/Users/conrad/0/amfs/test/')) {
    let doc = await conn.open(
      textDoc.uri.fsPath.replace('/Users/conrad/0/amfs/test/', '')
    )
    docs[textDoc.uri.toString()] = doc
    console.log('remote doc has....', doc.doc.content.toString())
  }
}

const closeTextDoc = async (textDoc: vscode.TextDocument) => {
  console.log(
    'did close text document',
    textDoc.uri.scheme,
    textDoc.uri.toString()
  )
  let doc = docs[textDoc.uri.toString()]
  if (doc) {
    delete docs[textDoc.uri.toString()]
    await conn.close(doc.amid)
  }
}

const changeTextDoc = (event: vscode.TextDocumentChangeEvent) => {
  var uri = event.document.uri.toString()
  console.log('change', event)

  // intercept when an untitled document is saved and copy over history.
  if (
    vscode.window.activeTextEditor?.document?.uri?.scheme === 'untitled' &&
    event.document.uri.scheme !== 'untitled' &&
    event.contentChanges.length === 1 &&
    event.contentChanges[0].text ===
      vscode.window.activeTextEditor?.document.getText() &&
    docs[uri].doc.content.toString() === ''
  ) {
    docs[uri] = docs[vscode.window.activeTextEditor?.document?.uri?.toString()]
    return
  }

  docs[uri].edit(event.contentChanges)

  console.log(
    'changed',
    event.document.uri.toString(),
    event.document.isUntitled,
    event.reason,
    docs[uri].doc.content.toString()
  )
}

// This method is called when your extension is activated
// Your extension is activated the very first time the command is executed
export function activate (context: vscode.ExtensionContext) {
  // Use the console to output diagnostic information (console.log) and errors (console.error)
  // This line of code will only be executed once when your extension is activated
  console.log('Congratulations, your extension "amfs" is now active!')

  context.subscriptions.push(
    vscode.workspace.onDidChangeTextDocument(changeTextDoc),
    vscode.workspace.onDidOpenTextDocument(openTextDoc),
    vscode.workspace.onDidCloseTextDocument(closeTextDoc)
  )
}

// This method is called when your extension is deactivated
export function deactivate () {}

/*

  let targets: { [key: number]: vscode.Position } = {}
  let id = 1

  let disposable = vscode.workspace.onDidChangeTextDocument(async event => {
    changeTextDoc(event)
    let version = event.document.version
    for (let c of event.contentChanges) {
      if (c.text === 'b') {
        console.log(
          version,
          'got b',
          c.range.start,
          vscode.window.activeTextEditor?.selection.start
        )
      }

      for (let id in targets) {
        if (
          c.range.start.line === targets[id].line &&
          c.range.start.character <= targets[id].character
        ) {
          targets[id] = new vscode.Position(
            targets[id].line,
            targets[id].character + c.text.length - c.rangeLength
          )
        }
      }
      if (c.text !== 'a' && c.text !== 'c') {
        continue
      }
      console.log(
        version,
        'got ' + c.text,
        c.range.start,
        vscode.window.activeTextEditor?.selection.start
      )

      let myid = id++
      targets[myid] = c.range.start

      setTimeout(async () => {
        console.log(
          version,
          'about to replace',
          event.document.version,
          targets[myid]
        )
        let success = await vscode.window.activeTextEditor?.edit(builder => {
          console.log(version, 'in callback')
          builder.replace(new vscode.Range(targets[myid], targets[myid]), 'b')
          delete targets[myid]
        })
        console.log(version, 'success?', success, event.document.version)
      }, 10)
    }
  })

*/
