# nmtree

[![Build Status](https://travis-ci.org/imsnif/nmtree.svg?branch=master)](https://travis-ci.org/imsnif/nmtree) [![Coverage Status](https://coveralls.io/repos/github/imsnif/nmtree/badge.svg?branch=master)](https://coveralls.io/github/imsnif/nmtree?branch=master) [![JavaScript Style Guide](https://img.shields.io/badge/code_style-standard-brightgreen.svg)](https://standardjs.com)

Get a node_modules directory with all its `package.json` files as a parsable flat tree.

![alt text](https://github.com/imsnif/nmtree/raw/master/docs/tty.gif )

### what is this?
Given an npm library, this tool would recursively go through its `node_modules` and create a flat tree with the paths of libraries as keys and their parsed `package.json` files as values.
eg.
```javascript
{
  "myLib": <myPackageJson>,
  "myLib/node_modules/myDep": <depPackageJson>,
  "myLib/node_modules/myDep/node_modules/myOtherDep": <otherDepPackageJson>
}
```
### install
`npm install -g nmtree` - for the cli tool

`npm install nmtree` for the `require`-able library

### usage
```javascript
const nmtree = require('nmtree')

const libPath = '/path/to/my/lib'
const tree = nmtree(libPath)

const installedReactVersions = Object.keys(tree).reduce((versions, libPath) => {
  const { name, version } = tree[libPath]
  if (name === 'react') versions.push(version)
  return versions
}, [])
// or whatever else you can think of!
```

### command line usage
```
nmtree /path/to/my/lib > my-lib-node-modules.json
```

### License
MIT

