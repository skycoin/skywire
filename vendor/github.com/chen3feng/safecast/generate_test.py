#!/usr/bin/env python3


from safecast import ALL_INT_BITS, to_camel_case


print('''
package safecast_test

import (
	_ "fmt"
	"math"
	"testing"

	"github.com/chen3feng/safecast"
)

func expectFalse(t * testing.T, value bool) {
    if value {
	    t.Helper()
        t.Errorf("Expect false, got true")
    }
}

func expectTrue(t * testing.T, value bool) {
    if !value {
        t.Helper()
        t.Errorf("Expect true, got false")
    }
}
''')

for from_bit in ALL_INT_BITS:
    for to_bit in ALL_INT_BITS:
        to_type = f'uint{to_bit}'
        from_type = f'int{from_bit}'
        print(f'''
    func TestTo_{from_type}_to_{to_type}(t * testing.T) {{
        var i {from_type} = -1
        _, ok := safecast.To[{to_type}](i)
        expectFalse(t, ok)
        i = 1
        _, ok = safecast.To[{to_type}](i)
        expectTrue(t, ok)
    }}''')

for from_bit in ALL_INT_BITS:
    for to_bit in ALL_INT_BITS:
        from_type = f'int{from_bit}'
        to_type = f'int{to_bit}'
        print(f'''
    func TestTo_{from_type}_to_{to_type}(t * testing.T) {{
        var i {from_type} = -1
        _, ok := safecast.To[{to_type}](i)
        expectTrue(t, ok)
        i = 1
        _, ok = safecast.To[{to_type}](i)
        expectTrue(t, ok)
    }}''')

for from_bit in ALL_INT_BITS:
    for to_bit in ALL_INT_BITS:
        from_type = f'uint{from_bit}'
        to_type = f'int{to_bit}'
        print(f'''
    func TestTo_{from_type}_to_{to_type}(t * testing.T) {{
        var i {from_type} = 1
        _, ok := safecast.To[{to_type}](i)
        expectTrue(t, ok)''')
        if from_bit and to_bit and int(from_bit) >= int(to_bit):
            print(f'''
        i = math.MaxInt{to_bit} + 1
        _, ok = safecast.To[{to_type}](i)
        expectFalse(t, ok)''')
        print('}')

for from_bit in ALL_INT_BITS:
    for to_bit in ALL_INT_BITS:
        from_type = f'uint{from_bit}'
        to_type = f'uint{to_bit}'
        print(f'''
    func TestTo_{from_type}_to_{to_type}(t * testing.T) {{
        var i {from_type} = 1
        _, ok := safecast.To[{to_type}](i)
        expectTrue(t, ok)''')
        if from_bit and to_bit and int(from_bit) > int(to_bit) and int(to_bit) < 64:
            print(f'''
        i = math.MaxUint{to_bit} + 1
        _, ok = safecast.To[{to_type}](i)
        expectFalse(t, ok)''')
        print('}')

for from_bit in ALL_INT_BITS:
    for to_bit in (32, 64):
        to_type = f'float{to_bit}'
        for int_type in ('int', 'uint'):
            from_type = f'{int_type}{from_bit}'
            print(f'''
        func TestTo_{from_type}_to_{to_type}(t * testing.T) {{
            var i {from_type} = 1
            _, ok := safecast.To[{to_type}](i)
            expectTrue(t, ok)''')
            print('}')

for from_bit in (32, 64):
    for to_bit in ALL_INT_BITS:
        from_type = f'float{from_bit}'
        to_type = f'int{to_bit}'
        print(f'''
        func TestTo_{from_type}_to_{to_type}(t * testing.T) {{
            var i {from_type} = 1
            _, ok := safecast.To[{to_type}](i)
            expectTrue(t, ok)
            i = math.Max{to_camel_case(to_type)}
            i *= 2
            _, ok = safecast.To[{to_type}](i)
            expectFalse(t, ok)''')
        print('}')

for from_bit in (32, 64):
    for to_bit in ALL_INT_BITS:
        from_type = f'float{from_bit}'
        to_type = f'uint{to_bit}'
        print(f'''
        func TestTo_{from_type}_to_{to_type}(t * testing.T) {{
            var i {from_type} = -1
            _, ok := safecast.To[{to_type}](i)
            expectFalse(t, ok)
            i = math.Max{to_camel_case(to_type)}
            i *= 2
            _, ok = safecast.To[{to_type}](i)
            expectFalse(t, ok)
            i = 1
            _, ok = safecast.To[{to_type}](i)
            expectTrue(t, ok)''')
        print('}')

for from_bit in (32, 64):
    for to_bit in (32, 64):
        from_type = f'float{from_bit}'
        to_type = f'float{to_bit}'
        print(f'''
        func TestTo_{from_type}_to_{to_type}(t * testing.T) {{
            var i {from_type} = -1
            _, ok := safecast.To[{to_type}](i)
            expectTrue(t, ok)
            i = 1
            _, ok = safecast.To[{to_type}](i)
            expectTrue(t, ok)''')
        print('}')
