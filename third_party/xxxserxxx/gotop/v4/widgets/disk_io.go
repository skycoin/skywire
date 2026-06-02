package widgets

import (
	"fmt"
	"log"
	"strings"
	"time"

	psDisk "github.com/shirou/gopsutil/v3/disk"

	"github.com/skycoin/skywire/third_party/VictoriaMetrics/metrics"
	ui "github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/termui"
	"github.com/skycoin/skywire/third_party/xxxserxxx/gotop/v4/utils"
)

// DiskIOWidget shows aggregate disk read/write throughput over time as two
// auto-scaling sparklines (multiload-ng style), in contrast to the default
// per-partition usage table (DiskWidget). Used for the "disk" slot under the
// global multiload display flag.
type DiskIOWidget struct {
	*ui.SparklineGroup
	updateInterval time.Duration
	totalRead      uint64
	totalWrite     uint64
	readMetric     *metrics.Counter
	writeMetric    *metrics.Counter
}

func NewDiskIOWidget() *DiskIOWidget {
	read := ui.NewSparkline()
	read.Data = []int{}
	write := ui.NewSparkline()
	write.Data = []int{}

	self := &DiskIOWidget{
		SparklineGroup: ui.NewSparklineGroup(read, write),
		updateInterval: time.Second,
	}
	self.Title = tr.Value("widget.label.disk")

	self.update()
	go func() {
		for range time.NewTicker(self.updateInterval).C {
			self.Lock()
			self.update()
			self.Unlock()
		}
	}()
	return self
}

func (d *DiskIOWidget) EnableMetric() {
	d.readMetric = metrics.NewCounter(makeName("disk", "read"))
	d.writeMetric = metrics.NewCounter(makeName("disk", "write"))
}

// aggregateDiskIO sums cumulative read/write bytes across whole physical disks,
// skipping partitions (a device whose name extends another device's name, e.g.
// sda1 under sda, nvme0n1p1 under nvme0n1) so the totals aren't double counted.
func aggregateDiskIO() (read, write uint64) {
	counters, err := psDisk.IOCounters()
	if err != nil {
		log.Print(err)
		return 0, 0
	}
	names := make([]string, 0, len(counters))
	for name := range counters {
		names = append(names, name)
	}
	isPartition := func(name string) bool {
		for _, other := range names {
			if other != name && strings.HasPrefix(name, other) {
				return true
			}
		}
		return false
	}
	for name, c := range counters {
		if isPartition(name) {
			continue
		}
		read += c.ReadBytes
		write += c.WriteBytes
	}
	return read, write
}

func (d *DiskIOWidget) update() {
	read, write := aggregateDiskIO()
	if d.totalRead != 0 || d.totalWrite != 0 { // not the first sample
		// compare before subtracting so a counter reset (read < previous) can't
		// wrap the unsigned delta into a huge spike.
		var recentRead, recentWrite uint64
		if read >= d.totalRead {
			recentRead = read - d.totalRead
		}
		if write >= d.totalWrite {
			recentWrite = write - d.totalWrite
		}
		rr := int(recentRead)  //nolint:gosec // per-second byte delta, never overflows int64
		rw := int(recentWrite) //nolint:gosec // per-second byte delta, never overflows int64

		d.Lines[0].Data = append(d.Lines[0].Data, rr)
		d.Lines[1].Data = append(d.Lines[1].Data, rw)
		if d.readMetric != nil {
			d.readMetric.Add(rr)
			d.writeMetric.Add(rw)
		}

		recentReadConv, recentReadUnit := utils.ConvertBytes(recentRead)
		recentWriteConv, recentWriteUnit := utils.ConvertBytes(recentWrite)
		totalReadConv, totalReadUnit := utils.ConvertBytes(read)
		totalWriteConv, totalWriteUnit := utils.ConvertBytes(write)

		d.Lines[0].Title1 = fmt.Sprintf(" Total R: %5.1f %s", totalReadConv, totalReadUnit)
		d.Lines[0].Title2 = fmt.Sprintf(" R/s: %9.1f %2s/s", recentReadConv, recentReadUnit)
		d.Lines[1].Title1 = fmt.Sprintf(" Total W: %5.1f %s", totalWriteConv, totalWriteUnit)
		d.Lines[1].Title2 = fmt.Sprintf(" W/s: %9.1f %2s/s", recentWriteConv, recentWriteUnit)
	}

	d.totalRead = read
	d.totalWrite = write
}
