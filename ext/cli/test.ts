import * as sync from '../src/sync'
import * as vscode from 'vscode'

async function go () {
  let conn = new sync.Connection()

  let doc = await conn.open('a')

  console.log('opened', 'a', doc.amid)
  console.log('>', doc.doc.content.toString())

  await doc.edit([
    {
      rangeLength: 0,
      rangeOffset: 1,
      text: '@'
    }
  ])
}

go()
