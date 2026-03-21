package idxdb

import (
	"os"
	"testing"
)

func TestNewDriveIdxDB(t *testing.T) {

	t.Run("cant open", func(t *testing.T) {
		defer os.Remove(testFileName)

		fl, err := os.Create(testFileName)
		if err != nil {
			t.Error(err)
			return
		}
		defer fl.Close()

		if _, err := fl.Write([]byte("Abra-Cadabra")); err != nil {
			t.Error(err)
			return
		}

		if idx, err := NewDriveIdxDB(testFileName); err == nil {
			t.Error("missing error")
			idx.Close()
		}
	})

	// It's impossible to test

	// t.Run("cant create bucket", func(t *testing.T) {
	// })

}

func Test_incSlice(t *testing.T) {
	x := []byte{0, 0xff}
	incSlice(x)
	if x[0] != 0x01 || x[1] != 0x00 {
		t.Error("wrong")
	}
}
