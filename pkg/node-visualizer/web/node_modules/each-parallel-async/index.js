/*!
 * each-parallel-async <https://github.com/jonschlinkert/each-parallel-async>
 *
 * Copyright (c) 2017, Jon Schlinkert.
 * Released under the MIT License.
 */

'use strict';

module.exports = each;
function each(arr, next, cb) {
  if (typeof cb !== 'function') {
    throw new TypeError('expected callback to be a function');
  }
  if (typeof next !== 'function') {
    cb(new TypeError('expected iteratee to be a function'));
    return;
  }
  if (!Array.isArray(arr)) {
    cb(new TypeError('expected the first argument to be an array'));
    return;
  }

  var len = arr.length;
  if (len === 0) {
    cb(null, arr);
    return;
  }

  var error = null;
  var num = 0;

  for (var i = 0; i < arr.length; i++) {
    try {
      next(arr[i], invoke(), i, arr);
    } catch (err) {
      cb(err);
      break;
    }

    if (error) {
      break;
    }
  }

  function invoke() {
    return function(err, val) {
      if (error) return;
      if (err) {
        error = err;
        cb(err);
        return;
      }
      if (++num === arr.length) {
        cb();
      }
    };
  }
};
