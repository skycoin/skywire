#!/usr/bin/env python3

"""Generate the generic 'To' function."""

from safecast import ALL_INT_BITS, to_camel_case


def generate_header():
    """Generate file header."""
    print('package safecast\n')
    print("""
type numericType interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
	~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
	~float32 | ~float64
}"""
          )


def generate_to_function():
    """Generate int conversion functions."""

    print("""
// To converts a numeric value from the FromType to the specified ToType type safely.
// result will always be same as the usual type cast (type(value)),
// but ok is false when overflow or underflow occured.
func To[ToType numericType, FromType numericType](value FromType)(result ToType, ok bool) {
    ok = true
    switch t := any(result).(type) {""")
    for to_type in ('int', 'uint'):
        for to_bits in ALL_INT_BITS:
            print(f'\tcase {to_type}{to_bits}:')
            print(f'\t\tt, ok = To{to_camel_case(to_type)}{to_bits}(value)')
            print(f'\t\tresult = ToType(t)')
    for to_bits in (32, 64):
        print(f'\tcase float{to_bits}:')
        print(f'\t\tt, ok = ToFloat{to_bits}(value)')
        print(f'\t\tresult = ToType(t)')

    print('\t}\n\treturn result, ok')
    print('}\n')

    for to_type in ('int', 'uint'):
        for to_bits in ALL_INT_BITS:
            generate_to_type(to_type, to_bits)
    for to_bits in (32, 64):
        generate_to_type('float', to_bits)


def generate_to_type(to_type, to_bits):
    full_to_type = f'{to_type}{to_bits}'
    funcname = f'To{to_camel_case(to_type)}{to_bits}'
    print(f'// {funcname} converts value to {full_to_type} type safely.\n'
          f'// result will always be same as the usual type cast({full_to_type}(value)),\n'
          f'// but ok is false when overflow or underflow occured.'
          )
    print(
        f'func {funcname}[FromType numericType](value FromType) ({full_to_type}, bool) {{')
    print('\tvar zero FromType // Use zero to any for type switch to avoid malloc')
    print(f'\tswitch any(zero).(type) {{')
    for from_type in ('int', 'uint'):
        for from_bits in ALL_INT_BITS:
            full_from_type = f'{from_type}{from_bits}'
            generate_call(full_from_type, full_to_type)

    for from_bits in (32, 64):
        full_from_type = f'float{from_bits}'
        generate_call(full_from_type, full_to_type)
    print(f'\t}}\n\treturn {full_to_type}(value), false')
    print('}\n')


def generate_call(full_from_type, full_to_type):
    if full_from_type == full_to_type:
        print(f'\tcase {full_from_type}: return {full_to_type}(value), true')
    else:
        print(
            f'\tcase {full_from_type}: return {full_from_type}To{to_camel_case(full_to_type)}({full_from_type}(value))')


def main():
    generate_header()
    generate_to_function()


main()
