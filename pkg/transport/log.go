// Package transport pkg/transport/log.go
package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/gob"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gocarina/gocsv"
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

const dateFormat string = "2006-01-02"

// CsvEntry represents a logging entry for csv for a given Transport.
type CsvEntry struct {
	TpID uuid.UUID `csv:"tp_id"`
	// atomic requires 64-bit alignment for struct field access
	LogEntry
	TimeStamp int64 `csv:"time_stamp"` // TimeStamp should be time.RFC3339Nano formatted
}

// LogEntry represents a logging entry for a given Transport.
// The entry is updated every time a packet is received or sent.
type LogEntry struct {
	// atomic requires 64-bit alignment for struct field access
	RecvBytes *uint64 `csv:"recv"` // Total received bytes.
	SentBytes *uint64 `csv:"sent"` // Total sent bytes.
}

// MakeLogEntry makes a new LogEntry by adding the info from old entry if found
func MakeLogEntry(ls LogStore, tpID uuid.UUID, log *logging.Logger) *LogEntry {
	oldLogEntry, err := ls.Entry(tpID)
	if err != nil {
		log.Warn(err)
		log.Warn(fmt.Errorf("new log entry will create for transport %s", tpID.String()))
	}
	newEntry := NewLogEntry()
	if oldLogEntry != nil {
		newEntry.AddRecv(*oldLogEntry.RecvBytes)
		newEntry.AddSent(*oldLogEntry.SentBytes)
	}
	return newEntry
}

// NewLogEntry creates a new LogEntry
func NewLogEntry() *LogEntry {
	recv := uint64(0)
	sent := uint64(0)
	return &LogEntry{
		RecvBytes: &recv,
		SentBytes: &sent,
	}
}

// AddRecv records read.
func (le *LogEntry) AddRecv(n uint64) {
	atomic.AddUint64(le.RecvBytes, n)
}

// AddSent records write.
func (le *LogEntry) AddSent(n uint64) {
	atomic.AddUint64(le.SentBytes, n)
}

// Reset resets LogEntry.
func (le *LogEntry) Reset() {
	atomic.AddUint64(le.SentBytes, -*le.SentBytes)
	atomic.AddUint64(le.RecvBytes, -*le.RecvBytes)
}

// MarshalJSON implements json.Marshaller
func (le *LogEntry) MarshalJSON() ([]byte, error) {
	var rb uint64
	var sb uint64
	if le.RecvBytes != nil {
		rb = atomic.LoadUint64(le.RecvBytes)
	}
	if le.SentBytes != nil {
		sb = atomic.LoadUint64(le.SentBytes)
	}
	return []byte(`{"recv":` + fmt.Sprint(rb) + `,"sent":` + fmt.Sprint(sb) + `}`), nil
}

// GobEncode implements gob.GobEncoder
func (le *LogEntry) GobEncode() ([]byte, error) {
	var b bytes.Buffer
	enc := gob.NewEncoder(&b)
	if le.RecvBytes != nil {
		rb := atomic.LoadUint64(le.RecvBytes)
		if err := enc.Encode(rb); err != nil {
			return nil, err
		}
	}
	if le.SentBytes != nil {
		sb := atomic.LoadUint64(le.SentBytes)
		if err := enc.Encode(sb); err != nil {
			return nil, err
		}
	}
	return b.Bytes(), nil
}

// GobDecode implements gob.GobDecoder
func (le *LogEntry) GobDecode(b []byte) error {
	r := bytes.NewReader(b)
	dec := gob.NewDecoder(r)
	var rb uint64
	if err := dec.Decode(&rb); err != nil {
		return err
	}
	var sb uint64
	if err := dec.Decode(&sb); err != nil {
		return err
	}
	// Allocate pointers if nil (happens when decoding into a fresh struct)
	if le.RecvBytes == nil {
		le.RecvBytes = new(uint64)
	}
	atomic.StoreUint64(le.RecvBytes, rb)
	if le.SentBytes == nil {
		le.SentBytes = new(uint64)
	}
	atomic.StoreUint64(le.SentBytes, sb)
	return nil
}

// LogStore stores transport log entries.
type LogStore interface {
	Entry(id uuid.UUID) (*LogEntry, error)
	Record(id uuid.UUID, entry *LogEntry) error
}

type inMemoryTransportLogStore struct {
	entries map[uuid.UUID]*LogEntry
	mu      sync.Mutex
}

// InMemoryTransportLogStore implements in-memory TransportLogStore.
func InMemoryTransportLogStore() LogStore {
	return &inMemoryTransportLogStore{
		entries: make(map[uuid.UUID]*LogEntry),
	}
}

func (tls *inMemoryTransportLogStore) Entry(id uuid.UUID) (*LogEntry, error) {
	tls.mu.Lock()
	entry, ok := tls.entries[id]
	tls.mu.Unlock()
	if !ok {
		return nil, errors.New("transport log entry not found")
	}

	return entry, nil
}

func (tls *inMemoryTransportLogStore) Record(id uuid.UUID, entry *LogEntry) error {
	tls.mu.Lock()
	if tls.entries == nil {
		tls.entries = make(map[uuid.UUID]*LogEntry)
	}
	tls.entries[id] = entry
	tls.mu.Unlock()
	return nil
}

type fileTransportLogStore struct {
	dir      string
	log      *logging.Logger
	mu       sync.Mutex
	fileName string
}

// FileTransportLogStore implements file TransportLogStore.
func FileTransportLogStore(ctx context.Context, dir string, rInterval time.Duration, log *logging.Logger) (LogStore, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}

	fLogStore := &fileTransportLogStore{
		dir: dir,
		log: log,
	}

	go func() {
		ticker := time.NewTicker(time.Hour * 5)
		defer ticker.Stop()
		fLogStore.cleanLogs(rInterval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fLogStore.cleanLogs(rInterval)
			}
		}
	}()

	return fLogStore, nil
}

func (tls *fileTransportLogStore) Entry(tpID uuid.UUID) (*LogEntry, error) {
	tls.mu.Lock()
	defer tls.mu.Unlock()

	entries, err := tls.readFromCSV(tls.todayFileName())
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.TpID == tpID {
			return &entry.LogEntry, nil
		}
	}
	return nil, nil
}

func (tls *fileTransportLogStore) Record(tpID uuid.UUID, lEntry *LogEntry) error {
	tls.mu.Lock()
	defer tls.mu.Unlock()

	cEntry := &CsvEntry{
		TpID:      tpID,
		LogEntry:  *lEntry,
		TimeStamp: time.Now().UTC().Unix(),
	}

	return tls.writeToCSV(cEntry)
}

func (tls *fileTransportLogStore) writeToCSV(cEntry *CsvEntry) error {

	today := tls.todayFileName()
	// we check if the date of the file has changed or not
	// if it is then it means it's a new day so we need to reset the LogEntry
	// so that we can start the count again for the new day and file
	if tls.fileName != "" && tls.fileName != tls.todayFileName() {
		// before we reset we need to save the current data so we save it in the previous days file
		// note: the timestamp of this entry will likely be of the current day so if a log file has
		// a timestamp of next day then it is an indicator that it's an inter-day transport log
		today = tls.fileName
	}

	filePath := filepath.Join(tls.dir, today)

	//nolint:gosec
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}

	readClients := []*CsvEntry{}
	writeClients := []*CsvEntry{}

	if err := gocsv.UnmarshalFile(f, &readClients); err != nil && !errors.Is(err, gocsv.ErrEmptyCSVFile) {
		// Close the file before attempting recovery
		f.Close() //nolint:errcheck,gosec

		// Attempt to recover from corrupted CSV
		recovered, recoverErr := tls.recoverCSV(filePath)
		if recoverErr != nil {
			return fmt.Errorf("CSV parse error and recovery failed: %w (original: %v)", recoverErr, err)
		}
		readClients = recovered

		// Repair the file by rewriting it without corrupted lines
		if repairErr := tls.repairCSVFile(filePath, readClients); repairErr != nil {
			tls.log.WithError(repairErr).Warn("Failed to repair CSV file")
		}

		// Reopen the repaired file
		f, err = os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0600) //nolint:gosec
		if err != nil {
			return err
		}
	}

	defer func() {
		if err := f.Close(); err != nil {
			tls.log.WithError(err).Errorln("Failed to close csv file")
		}
	}()

	var update bool
	for _, client := range readClients {
		// update if readClients contains the cEntry
		if client.TpID == cEntry.TpID {
			writeClients = append(writeClients, cEntry)
			update = true
			continue
		}
		writeClients = append(writeClients, client)
	}

	// write when the readClients are does not contain cEntry
	if !update {
		writeClients = append(writeClients, cEntry)
	}

	if _, err := f.Seek(0, 0); err != nil { // Go to the start of the file
		return err
	}

	err = gocsv.MarshalFile(&writeClients, f) // Use this to save the CSV back to the file
	if err != nil {
		return err
	}

	// we reset the entry after it is saved
	if tls.fileName != "" && tls.fileName != tls.todayFileName() {
		cEntry.LogEntry.Reset()
	}

	tls.fileName = tls.todayFileName()

	return nil
}

func (tls *fileTransportLogStore) readFromCSV(fileName string) ([]*CsvEntry, error) {
	f, err := os.OpenFile(filepath.Join(tls.dir, fmt.Sprint(fileName)), os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := f.Close(); err != nil {
			tls.log.WithError(err).Errorln("Failed to close csv file")
		}
	}()

	readClients := []*CsvEntry{}

	if err := gocsv.UnmarshalFile(f, &readClients); err != nil && !errors.Is(err, gocsv.ErrEmptyCSVFile) { // Load clients from file
		return nil, err
	}
	return readClients, nil
}

// recoverCSV attempts to recover valid entries from a corrupted CSV file.
// It reads line-by-line, skipping any malformed lines, and returns valid entries.
func (tls *fileTransportLogStore) recoverCSV(filePath string) ([]*CsvEntry, error) {
	f, err := os.Open(filePath) //nolint:gosec // filePath is from internal config
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck

	var validLines []string
	scanner := bufio.NewScanner(f)
	lineNum := 0
	skippedLines := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Keep the header line
		if lineNum == 1 {
			validLines = append(validLines, line)
			continue
		}

		// Validate line has correct number of fields (4: tp_id, recv, sent, time_stamp)
		reader := csv.NewReader(strings.NewReader(line))
		fields, err := reader.Read()
		if err != nil || len(fields) != 4 {
			tls.log.Debugf("Skipping corrupted CSV line %d: %q", lineNum, line)
			skippedLines++
			continue
		}

		validLines = append(validLines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if skippedLines > 0 {
		tls.log.Infof("Recovered CSV: skipped %d corrupted line(s)", skippedLines)
	}

	// Parse the valid lines
	if len(validLines) <= 1 {
		return []*CsvEntry{}, nil
	}

	csvData := strings.Join(validLines, "\n")
	var entries []*CsvEntry
	if err := gocsv.UnmarshalString(csvData, &entries); err != nil {
		return nil, fmt.Errorf("failed to parse recovered CSV: %w", err)
	}

	return entries, nil
}

// repairCSVFile repairs a corrupted CSV file by removing invalid lines.
func (tls *fileTransportLogStore) repairCSVFile(filePath string, entries []*CsvEntry) error {
	// Write repaired content to a temp file
	tmpPath := filePath + ".tmp"
	tmpFile, err := os.Create(tmpPath) //nolint:gosec // tmpPath is derived from internal config
	if err != nil {
		return err
	}

	if err := gocsv.MarshalFile(&entries, tmpFile); err != nil {
		tmpFile.Close()    //nolint:errcheck,gosec
		os.Remove(tmpPath) //nolint:errcheck,gosec
		return err
	}
	tmpFile.Close() //nolint:errcheck,gosec

	// Replace original with repaired file
	return os.Rename(tmpPath, filePath)
}

// CleanLogs cleans the logs that are older than the given log rotation interval
func (tls *fileTransportLogStore) cleanLogs(rInterval time.Duration) {

	files, err := os.ReadDir(tls.dir)
	if err != nil {
		tls.log.Warn(err)
	}

	for _, file := range files {
		if !file.IsDir() {
			interval := time.Now().UTC().Add(-rInterval)
			date, err := time.Parse(dateFormat, strings.ReplaceAll(file.Name(), ".csv", ""))
			if err != nil {
				tls.log.Warn(err)
			}
			if date.Before(interval) {
				err = os.Remove(tls.dir + "/" + file.Name())
				if err != nil {
					tls.log.Warn(err)
				}
				tls.log.Debugf("transport log file cleaned: %v", file.Name())
			}
		}
	}
}

func (tls *fileTransportLogStore) todayFileName() string {
	return fmt.Sprintf("%s.csv", time.Now().UTC().Format(dateFormat))
}

// LatencyCsvEntry represents a logging entry for csv for transport latency.
type LatencyCsvEntry struct {
	TpID      uuid.UUID `csv:"tp_id"`
	MinMs     float64   `csv:"min_ms"`
	MaxMs     float64   `csv:"max_ms"`
	AvgMs     float64   `csv:"avg_ms"`
	TimeStamp int64     `csv:"time_stamp"`
}

// LatencyLogStore stores transport latency log entries.
type LatencyLogStore interface {
	Entry(id uuid.UUID) (*LatencyCsvEntry, error)
	Record(id uuid.UUID, min, max, avg float64) error
}

type inMemoryLatencyLogStore struct {
	entries map[uuid.UUID]*LatencyCsvEntry
	mu      sync.Mutex
}

// InMemoryLatencyLogStore implements in-memory LatencyLogStore.
func InMemoryLatencyLogStore() LatencyLogStore {
	return &inMemoryLatencyLogStore{
		entries: make(map[uuid.UUID]*LatencyCsvEntry),
	}
}

func (ls *inMemoryLatencyLogStore) Entry(id uuid.UUID) (*LatencyCsvEntry, error) {
	ls.mu.Lock()
	entry, ok := ls.entries[id]
	ls.mu.Unlock()
	if !ok {
		return nil, errors.New("latency log entry not found")
	}
	return entry, nil
}

func (ls *inMemoryLatencyLogStore) Record(id uuid.UUID, min, max, avg float64) error {
	ls.mu.Lock()
	if ls.entries == nil {
		ls.entries = make(map[uuid.UUID]*LatencyCsvEntry)
	}
	ls.entries[id] = &LatencyCsvEntry{
		TpID:      id,
		MinMs:     min,
		MaxMs:     max,
		AvgMs:     avg,
		TimeStamp: time.Now().UTC().Unix(),
	}
	ls.mu.Unlock()
	return nil
}

type fileLatencyLogStore struct {
	dir      string
	log      *logging.Logger
	mu       sync.Mutex
	fileName string
}

// FileLatencyLogStore implements file LatencyLogStore.
func FileLatencyLogStore(ctx context.Context, dir string, rInterval time.Duration, log *logging.Logger) (LatencyLogStore, error) {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}

	fLogStore := &fileLatencyLogStore{
		dir: dir,
		log: log,
	}

	go func() {
		ticker := time.NewTicker(time.Hour * 5)
		defer ticker.Stop()
		fLogStore.cleanLogs(rInterval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fLogStore.cleanLogs(rInterval)
			}
		}
	}()

	return fLogStore, nil
}

func (ls *fileLatencyLogStore) Entry(tpID uuid.UUID) (*LatencyCsvEntry, error) {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	entries, err := ls.readFromCSV(ls.todayFileName())
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.TpID == tpID {
			return entry, nil
		}
	}
	return nil, nil
}

func (ls *fileLatencyLogStore) Record(tpID uuid.UUID, min, max, avg float64) error {
	ls.mu.Lock()
	defer ls.mu.Unlock()

	entry := &LatencyCsvEntry{
		TpID:      tpID,
		MinMs:     min,
		MaxMs:     max,
		AvgMs:     avg,
		TimeStamp: time.Now().UTC().Unix(),
	}

	return ls.writeToCSV(entry)
}

func (ls *fileLatencyLogStore) writeToCSV(entry *LatencyCsvEntry) error {
	today := ls.todayFileName()
	if ls.fileName != "" && ls.fileName != ls.todayFileName() {
		today = ls.fileName
	}

	filePath := filepath.Join(ls.dir, today)

	//nolint:gosec
	f, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return err
	}

	defer func() {
		if err := f.Close(); err != nil {
			ls.log.WithError(err).Errorln("Failed to close latency csv file")
		}
	}()

	readEntries := []*LatencyCsvEntry{}
	writeEntries := []*LatencyCsvEntry{}

	if err := gocsv.UnmarshalFile(f, &readEntries); err != nil && !errors.Is(err, gocsv.ErrEmptyCSVFile) {
		ls.log.WithError(err).Warn("Failed to parse latency CSV, starting fresh")
		readEntries = []*LatencyCsvEntry{}
	}

	var update bool
	for _, existing := range readEntries {
		if existing.TpID == entry.TpID {
			writeEntries = append(writeEntries, entry)
			update = true
			continue
		}
		writeEntries = append(writeEntries, existing)
	}

	if !update {
		writeEntries = append(writeEntries, entry)
	}

	if _, err := f.Seek(0, 0); err != nil {
		return err
	}

	if err := f.Truncate(0); err != nil {
		return err
	}

	if err := gocsv.MarshalFile(&writeEntries, f); err != nil {
		return err
	}

	ls.fileName = ls.todayFileName()
	return nil
}

func (ls *fileLatencyLogStore) readFromCSV(fileName string) ([]*LatencyCsvEntry, error) {
	f, err := os.OpenFile(filepath.Join(ls.dir, fileName), os.O_RDWR|os.O_CREATE, 0600) //nolint:gosec
	if err != nil {
		return nil, err
	}

	defer func() {
		if err := f.Close(); err != nil {
			ls.log.WithError(err).Errorln("Failed to close latency csv file")
		}
	}()

	entries := []*LatencyCsvEntry{}
	if err := gocsv.UnmarshalFile(f, &entries); err != nil && !errors.Is(err, gocsv.ErrEmptyCSVFile) {
		return nil, err
	}
	return entries, nil
}

func (ls *fileLatencyLogStore) cleanLogs(rInterval time.Duration) {
	files, err := os.ReadDir(ls.dir)
	if err != nil {
		ls.log.Warn(err)
		return
	}

	for _, file := range files {
		if !file.IsDir() {
			interval := time.Now().UTC().Add(-rInterval)
			date, err := time.Parse(dateFormat, strings.ReplaceAll(file.Name(), ".csv", ""))
			if err != nil {
				ls.log.Warn(err)
				continue
			}
			if date.Before(interval) {
				err = os.Remove(filepath.Join(ls.dir, file.Name()))
				if err != nil {
					ls.log.Warn(err)
				}
				ls.log.Debugf("latency log file cleaned: %v", file.Name())
			}
		}
	}
}

func (ls *fileLatencyLogStore) todayFileName() string {
	return fmt.Sprintf("%s.csv", time.Now().UTC().Format(dateFormat))
}
