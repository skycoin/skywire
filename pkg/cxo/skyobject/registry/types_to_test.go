package registry

// dummy types

type TestIntsStruct struct {
	Int8  int8
	Int16 int16
	Int32 int32
	Int64 int64
}

type TestNamedInt8 int8
type TestNamedInt16 int16
type TestNamedInt32 int32
type TestNamedInt64 int64

type TestNamedIntsStruct struct {
	Int8  TestNamedInt8
	Int16 TestNamedInt16
	Int32 TestNamedInt32
	Int64 TestNamedInt64
}

type TestUintsStruct struct {
	Uint8  uint8
	Uint16 uint16
	Uint32 uint32
	Uint64 uint64
}

type TestNamedUint8 uint8
type TestNamedUint16 uint16
type TestNamedUint32 uint32
type TestNamedUint64 uint64

type TestNamedUintsStruct struct {
	Uint8  TestNamedUint8
	Uint16 TestNamedUint16
	Uint32 TestNamedUint32
	Uint64 TestNamedUint64
}

type TestStringStruct struct {
	String string
}

type TestNamedString string

type TestNamedStringStruct struct {
	String TestNamedString
}

type TestFloatsStruct struct {
	Float32 float32
	Float64 float64
}

type TestNamedFloat32 float32
type TestNamedFloat64 float64

type TestNamedFloatsStruct struct {
	Float32 TestNamedFloat32
	Float64 TestNamedFloat64
}

type TestNamedArray [5]int32

type TestEmptyStruct struct{}

type TestArraysStruct struct {
	ZeroInt8         [0]int8
	OneInt16         [1]int16
	TwoStrings       [2]string
	ThreeEmptyStruct [3]TestEmptyStruct
	FourStringStruct [4]TestStringStruct
	Named            TestNamedArray
}

type TestNamedSlice []int32

type TestSliceStruct struct {
	Int8         []int8
	Named        TestNamedSlice
	String       []string
	EmptyStruct  []TestEmptyStruct
	StringStruct []TestStringStruct
}

type TestUser struct {
	Name   string
	Age    uint32
	Hidden []byte `enc:"-"` // only local
}

type TestGroup struct {
	Name      string
	Members   Refs    `skyobject:"schema=test.User"`
	Curator   Ref     `skyobject:"schema=test.User"`
	Developer Dynamic // User or Man
}

type TestMan struct {
	Name   string
	GitHub string
}

type testType struct {
	Name string
	Val  interface{}
}

// name -> val to register and encode/decode/size test
func testTypes() (ts []testType) {
	ts = []testType{
		{"test.Ints", TestIntsStruct{8, 16, 32, 64}},
		{"test.NamedInts", TestNamedIntsStruct{8, 16, 32, 64}},
		{"test.Uints", TestUintsStruct{8, 16, 32, 64}},
		{"test.NamedUints", TestNamedUintsStruct{8, 16, 32, 64}},
		{"test.String", TestStringStruct{"string"}},
		{"test.NamedString", TestNamedStringStruct{"named string"}},
		{"test.Floats", TestFloatsStruct{32.0, 64.0}},
		{"test.NamedFloats", TestNamedFloatsStruct{32.0, 64.0}},
		{"test.Empty", TestEmptyStruct{}},
		{"test.Arrays", TestArraysStruct{
			[0]int8{},
			[1]int16{16},
			[2]string{"string", "string"},
			[3]TestEmptyStruct{},
			[4]TestStringStruct{{"string"}, {"string"}, {"string"}, {"string"}},
			TestNamedArray{10, 11, 12, 13, 14},
		}},
		{"test.Slices", TestSliceStruct{
			[]int8{8},
			TestNamedSlice{32},
			[]string{"string", "string"},
			[]TestEmptyStruct{},
			[]TestStringStruct{{"string"}, {"string"}, {"string"}, {"string"}},
		}},
		{"test.User", TestUser{"Alice", 15, []byte("hey-ho!")}},
		{"test.Group", TestGroup{Name: "the CXO"}},
		{"test.Man", TestMan{"kostyarin", "logrusorgru"}},
	}

	return
}

func testRegistry() (reg *Registry) {

	reg = NewRegistry(func(r *Reg) {

		for _, tt := range testTypes() {
			r.Register(tt.Name, tt.Val)
		}

	})

	return
}
