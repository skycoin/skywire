#!/usr/bin/env python3

from safecast import ALL_INT_BITS, ALL_FIXED_INT_BITS, to_camel_case


def generate_header():
    print('package safecast\n')
    print('import "math"\n')
    print('const intBits = 32 << (^uint(0) >> 63)\n')


def generate_fixed_int(from_type, from_bits, to_type, to_bits):
    full_from_type = f'{from_type}{from_bits}'
    full_to_type = f'{to_type}{to_bits}'

    no_overflow = f'{full_from_type}({full_to_type}(value)) == value'

    def same_signedness():
        if int(from_bits) <= int(to_bits):
            return
        return no_overflow

    def uint_to_int():
        if int(from_bits) < int(to_bits):
            return
        if int(from_bits) > int(to_bits):
            return no_overflow
        else:
            return f'value <= math.Max{to_camel_case(full_to_type)}'

    def int_to_uint():
        cond = 'value >= 0'
        if from_bits and to_bits:  # intnn to uintxx
            if int(from_bits) <= int(to_bits):
                return cond
            return f'{cond} && {no_overflow}'

    def range_chack():
        if from_type == to_type:
            return same_signedness()
        elif from_type == 'uint':
            return uint_to_int()
        else:  # int to uint
            return int_to_uint()

    print_function_header(full_from_type, full_to_type)
    ok = range_chack()
    print_function_footer(full_to_type, ok)


def print_function_header(full_from_type, full_to_type):
    func_name = f'{full_from_type}To{to_camel_case(full_to_type)}'
    print(f'// {func_name} converts the {full_from_type} value to {full_to_type} safely.')
    print(f'func {func_name}(value {full_from_type}) (result {full_to_type}, ok bool) {{')


def print_function_footer(full_to_type, ok):
    if not ok:
        ok = 'true'
    print(f'\treturn {full_to_type}(value), {ok}')
    print('}\n')


def generate_int_convs():
    """Generate int conversion functions."""
    for from_type in ('int', 'uint'):
        for from_bits in ALL_FIXED_INT_BITS:
            for to_type in ('int', 'uint'):
                for to_bits in ALL_FIXED_INT_BITS:
                    generate_fixed_int(from_type, from_bits, to_type, to_bits)
    for from_type in ('int', 'uint'):
        for from_bits in ALL_FIXED_INT_BITS:
            for to_type in ('int', 'uint'):
                generate_fixed_int_to_int(from_type, from_bits, to_type)
    for from_type in ('int', 'uint'):
        for to_type in ('int', 'uint'):
            for to_bit in ALL_FIXED_INT_BITS:
                generate_int_to_fixed_int(from_type, to_type, to_bit)

    print_function_header('int', 'uint')
    print('''    return uint(value), value >= 0''')
    print('}')

    print_function_header('uint', 'int')
    print('''    return int(value), value <= math.MaxInt''')
    print('}')


def generate_fixed_int_to_int(from_type, from_bits, to_type):
    full_from_type = f'{from_type}{from_bits}'
    print_function_header(full_from_type, to_type)
    print(f'''\
        if intBits == 32 {{
            var r {to_type}32
            r, ok = {full_from_type}To{to_camel_case(to_type)}32(value)
            result = {to_type}(r)
        }}
        var r {to_type}64
        r, ok =  {full_from_type}To{to_camel_case(to_type)}64(value)
        result = {to_type}(r)
        return''')
    print('}')


def generate_int_to_fixed_int(from_type, to_type, to_bits):
    full_to_type = f'{to_type}{to_bits}'
    print_function_header(from_type, full_to_type)
    print(f'''\
        if intBits == 32 {{
            return {from_type}32To{to_camel_case(full_to_type)}({from_type}32(value))
        }}
        return {from_type}64To{to_camel_case(full_to_type)}({from_type}64(value))''')
    print('}')


def generate_float_convs():
    """Generate int conversion functions."""
    for float_bit in (32, 64):
        for int_type in ('int', 'uint'):
            for int_bits in ALL_INT_BITS:
                generate_float_to_conv(float_bit, int_type, int_bits)
                generate_to_float_conv(int_type, int_bits, float_bit)

    print("""
    func float32ToFloat64(value float32) (float64, bool) {
        return float64(value), true
    }

    func float64ToFloat32(value float64) (float32, bool) {
        return float32(value), value >= -math.MaxFloat32 && value <= math.MaxFloat32
    }
    """)


def generate_float_to_conv(from_bits, to_type, to_bits):
    full_from_type = f'float{from_bits}'
    full_to_type = f'{to_type}{to_bits}'

    def range_chack():
        min = f'math.Min{to_camel_case(full_to_type)}' if to_type == 'int' else '0'
        cond = f'value >= {min}'
        cond += f' && value <= math.Max{to_camel_case(full_to_type)}'
        return cond

    func_name = f'float{from_bits}To{to_camel_case(to_type)}{to_bits}'
    print(f'// {func_name} converts the {full_from_type} value to {full_to_type} safely.')
    print(f'func {func_name}(value {full_from_type}) (result {full_to_type}, ok bool) {{')
    ok = range_chack() or 'true'
    print(f'\treturn {full_to_type}(value), {ok}')
    print('}\n')


def generate_to_float_conv(from_type, from_bits, to_bits):
    full_from_type = f'{from_type}{from_bits}'
    full_to_type = f'float{to_bits}'
    print(f'func {full_from_type}To{to_camel_case(full_to_type)}(value {full_from_type}) ({full_to_type}, bool) {{')
    print(f'\treturn {full_to_type}(value), true')
    print('}')


def main():
    generate_header()
    generate_int_convs()
    generate_float_convs()


main()
