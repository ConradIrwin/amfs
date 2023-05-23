"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
const sync = require("../src/sync");
async function go() {
    let conn = new sync.Connection();
    let doc = await conn.open('a');
    console.log('opened', 'a', doc.amid);
    console.log('>', doc.doc.content.toString());
    await doc.edit([
        {
            rangeLength: 0,
            rangeOffset: 1,
            text: '@'
        }
    ]);
}
go();
//# sourceMappingURL=test.js.map