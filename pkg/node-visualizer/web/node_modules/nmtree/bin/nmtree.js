#!/usr/bin/env node
'use strict'

const fs = require('fs')
const program = require('commander')
const nmTree = require('../')

program
  .command('*', 'path to package (eg. /home/foo/code/bar)')
  .action(path => {
    if (fs.existsSync(path)) {
      const tree = nmTree(path)
      const treeJson = JSON.stringify(tree, false, 2)
      console.log(treeJson)
    } else {
      console.error(`${path} does not exist`)
      process.exit(2)
    }
  })

program.parse(process.argv)
