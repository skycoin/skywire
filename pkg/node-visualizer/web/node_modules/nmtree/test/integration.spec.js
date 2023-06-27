'use strict'

const path = require('path')
const test = require('tape')

const nmTree = require('../')

function removeCurrentDirFromKeys (tree) {
  return Object.keys(tree).reduce((nTree, key) => {
    const re = new RegExp(`^${__dirname}`)
    const newKey = key.replace(re, '')
    nTree[newKey] = tree[key]
    return nTree
  }, {})
}

test('correctly creates a representation of a node_modules folder', t => {
  t.plan(1)
  try {
    const testPath = path.join(__dirname, 'fixtures', 'test-package')
    const testSnapshotPath = path.join(testPath, '.snapshot.json')
    const testSnapshot = require(testSnapshotPath)
    const tree = nmTree(testPath)
    const normalizedTree = removeCurrentDirFromKeys(tree)
    t.deepEquals(normalizedTree, testSnapshot, 'tree matches snapshot')
  } catch (e) {
    t.fail(e.message)
  }
})

test('correctly creates a representation of a node_modules folder with scopes', t => {
  t.plan(1)
  try {
    const testPath = path.join(__dirname, 'fixtures', 'test-package-with-scopes')
    const testSnapshotPath = path.join(testPath, '.snapshot.json')
    const testSnapshot = require(testSnapshotPath)
    const tree = nmTree(testPath)
    const normalizedTree = removeCurrentDirFromKeys(tree)
    t.deepEquals(normalizedTree, testSnapshot, 'tree matches snapshot')
  } catch (e) {
    t.fail(e.message)
  }
})
