ALL_FIXED_INT_BITS = ('8', '16', '32', '64')
ALL_INT_BITS = ALL_FIXED_INT_BITS + ('',)


def to_camel_case(type):
    return type[0].upper() + type[1:]
