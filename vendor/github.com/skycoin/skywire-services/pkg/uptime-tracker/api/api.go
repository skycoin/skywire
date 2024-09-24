// Package api pkg/uptime-tracker/api/api.go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/go-echarts/go-echarts/v2/charts"
	"github.com/go-echarts/go-echarts/v2/opts"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/geo"
	"github.com/skycoin/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/metricsutil"
	"github.com/skycoin/skywire-utilities/pkg/netutil"

	"github.com/skycoin/skywire-services/internal/utmetrics"
	"github.com/skycoin/skywire-services/pkg/uptime-tracker/store"
)

const (
	rateLimiterRequests = 5
	rateLimiterWindow   = 1 * time.Minute
)

// API register all the API endpoints.
// It implements a net/http.Handler.
type API struct {
	http.Handler
	metrics                     utmetrics.Metrics
	reqsInFlightCountMiddleware *metricsutil.RequestsInFlightCountMiddleware
	store                       store.Store
	locDetails                  geo.LocationDetails
	startedAt                   time.Time

	uptimesResponseCache   store.UptimeResponse
	uptimesResponseCacheMu sync.RWMutex
	visorsCache            store.VisorsResponse
	visorsCacheMu          sync.RWMutex
	dailyUptimeCache       map[string]map[string]string
	dailyUptimeCacheMu     sync.RWMutex
	storeUptimesCutoff     int
	storeUptimesPath       string

	dmsgAddr    string
	DmsgServers []string
}

// PrivateAPI register all the PrivateAPI endpoints.
// It implements a net/http.Handler.
type PrivateAPI struct {
	http.Handler
	store     store.Store
	startedAt time.Time
}

// HealthCheckResponse is struct of /health endpoint
type HealthCheckResponse struct {
	BuildInfo   *buildinfo.Info `json:"build_info,omitempty"`
	StartedAt   time.Time       `json:"started_at,omitempty"`
	DmsgAddr    string          `json:"dmsg_address,omitempty"`
	DmsgServers []string        `json:"dmsg_servers,omitempty"`
}

// New constructs a new API instance.
func New(log logrus.FieldLogger, s store.Store, nonceStore httpauth.NonceStore, locDetails geo.LocationDetails,
	enableLoadTesting, enableMetrics bool, m utmetrics.Metrics, storeDataCutoff int, storeDataPath, dmsgAddr string) *API {
	if log == nil {
		log = logging.MustGetLogger("uptime_tracker")
	}

	api := &API{
		metrics:                     m,
		reqsInFlightCountMiddleware: metricsutil.NewRequestsInFlightCountMiddleware(),
		store:                       s,
		locDetails:                  locDetails,
		startedAt:                   time.Now(),
		storeUptimesCutoff:          storeDataCutoff,
		storeUptimesPath:            storeDataPath,
		dmsgAddr:                    dmsgAddr,
		DmsgServers:                 []string{},
	}

	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	if enableMetrics {
		r.Use(api.reqsInFlightCountMiddleware.Handle)
		r.Use(metricsutil.RequestDurationMiddleware)
	}

	r.Get("/update", sendGone)
	r.Get("/v2/update", sendGone)
	r.Get("/v3/update", sendGone)

	r.Group(func(r chi.Router) {
		// logged requests group
		r.Use(httputil.NewLogMiddleware(log))

		r.Group(func(r chi.Router) {
			// authenticated requests group
			if enableLoadTesting {
				r.Use(httpauth.MakeLoadTestingMiddleware(nonceStore))
			} else {
				r.Use(httpauth.MakeMiddleware(nonceStore))
			}
			r.Get("/v4/update", api.handleUpdate())
		})

		r.Group(func(r chi.Router) {
			// request rate limited group
			r.Use(httprate.LimitByIP(rateLimiterRequests, rateLimiterWindow))

			r.Get("/visors", api.handleVisors)
			r.Get("/uptimes", api.handleUptimes)
		})

		r.Get("/uptime/{pk}", api.handleUptime)
		r.Get("/health", api.health)
		r.Get("/dashboard", api.chart)

		nonceHandler := &httpauth.NonceHandler{Store: nonceStore}
		r.Get("/security/nonces/{pk}", nonceHandler.ServeHTTP)
	})

	api.Handler = r

	return api
}

func (api *API) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// RunBackgroundTasks is function which runs periodic background tasks of API.
func (api *API) RunBackgroundTasks(ctx context.Context, logger logrus.FieldLogger) {
	api.dailyRoutine(logger)
	cacheTicker := time.NewTicker(time.Minute * 5)
	defer cacheTicker.Stop()
	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()
	api.updateInternalState(ctx, logger)
	api.updateInternalCaches(logger)
	for {
		select {
		case <-ctx.Done():
			return
		case <-cacheTicker.C:
			api.updateInternalCaches(logger)
			api.dailyRoutine(logger)
		case <-ticker.C:
			api.updateInternalState(ctx, logger)
		}
	}
}

func (api *API) updateInternalCaches(logger logrus.FieldLogger) {
	err := api.updateUptimesCache()
	if err != nil {
		logger.WithError(err).Errorf("failed to update uptimes cache")
	}
	err = api.updateVisorsCache()
	if err != nil {
		logger.WithError(err).Errorf("failed to update visors cache")
	}
	err = api.updateDailyUptimesCache()
	if err != nil {
		logger.WithError(err).Errorf("failed to update daily uptimes cache")
	}
}

func (api *API) dailyRoutine(logger logrus.FieldLogger) {
	oldestEntry, err := api.store.GetOldestEntry()
	if err != nil {
		logger.WithError(err).Warn("unable to fetch oldest entry from db")
		return
	}

	from := oldestEntry.CreatedAt
	to := time.Now().AddDate(0, 0, -(api.storeUptimesCutoff))

	for to.After(from) {
		timeValue := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Now().Location())
		data, err := api.store.GetSpecificDayData(timeValue)
		if err != nil {
			logger.WithField("date", timeValue.Format("2006-01-02")).WithError(err).Warn("unable to fetch data specific date from db")
			return
		}
		err = api.storeDailyData(data, timeValue)
		if err != nil {
			logger.WithError(err).Warn("unable to save data to json file")
			return
		}
		err = api.store.DeleteEntries(data)
		if err != nil {
			logger.WithError(err).Warn("unable to delete old entries from db")
		}
		from = from.AddDate(0, 0, 1)
	}
}

func (api *API) storeDailyData(data []store.DailyUptimeHistory, timeValue time.Time) error {
	// check path, make its if not available
	os.MkdirAll(api.storeUptimesPath, os.ModePerm) //nolint
	// save to file
	file, _ := json.MarshalIndent(data, "", " ") //nolint
	fileName := fmt.Sprintf("%s/%s-uptime-data.json", api.storeUptimesPath, timeValue.Format("2006-01-02"))
	return os.WriteFile(fileName, file, 0644) //nolint
}

func (api *API) updateVisorsCache() error {
	visors, err := api.store.GetAllVisors(api.locDetails)
	if err != nil {
		return err
	}
	api.visorsCacheMu.Lock()
	defer api.visorsCacheMu.Unlock()
	api.visorsCache = visors
	return nil
}

func (api *API) getVisors() store.VisorsResponse {
	api.visorsCacheMu.RLock()
	defer api.visorsCacheMu.RUnlock()
	return api.visorsCache
}
func (api *API) getVisorsLen() int {
	api.visorsCacheMu.RLock()
	defer api.visorsCacheMu.RUnlock()
	return len(api.visorsCache)
}

func (api *API) updateUptimesCache() error {
	now := time.Now()
	// by default, we're fetching data for the current month
	startYear, startMonth, endYear, endMonth := now.Year(), now.Month(), now.Year(), now.Month()
	uptimes, err := api.store.GetAllUptimes(startYear, startMonth, endYear, endMonth)
	if err != nil {
		return err
	}
	api.visorsCacheMu.Lock()
	defer api.visorsCacheMu.Unlock()
	api.uptimesResponseCache = uptimes
	return nil
}

func (api *API) updateDailyUptimesCache() error {
	dailyHistory, err := api.store.GetDailyUpdateHistory()
	if err != nil {
		return err
	}
	api.dailyUptimeCacheMu.Lock()
	defer api.dailyUptimeCacheMu.Unlock()
	api.dailyUptimeCache = dailyHistory
	return nil
}

func (api *API) getAllUptimes() store.UptimeResponse {
	api.uptimesResponseCacheMu.RLock()
	defer api.uptimesResponseCacheMu.RUnlock()
	return api.uptimesResponseCache
}

func (api *API) getAllUptimesLen() int {
	api.uptimesResponseCacheMu.RLock()
	defer api.uptimesResponseCacheMu.RUnlock()
	return len(api.uptimesResponseCache)
}

func (api *API) getDailyUptimes() map[string]map[string]string {
	api.dailyUptimeCacheMu.RLock()
	defer api.dailyUptimeCacheMu.RUnlock()
	return api.dailyUptimeCache
}

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

// ServeHTTP implements http.Handler.
func (api *API) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var status int

	if err == context.DeadlineExceeded {
		status = http.StatusRequestTimeout
	}

	// we still haven't found the error
	if status == 0 {
		if _, ok := err.(*json.SyntaxError); ok {
			status = http.StatusBadRequest
		}
	}

	// we fallback to 500
	if status == 0 {
		status = http.StatusInternalServerError
	}

	if status != http.StatusNotFound {
		api.log(r).Warnf("%d: %s", status, err)
	}

	w.WriteHeader(status)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(&Error{Error: err.Error()}); err != nil {
		api.log(r).WithError(err).Warn("Failed to encode error")
	}
}

func (api *API) handleVisors(w http.ResponseWriter, r *http.Request) {
	var err error
	var visors store.VisorsResponse

	if api.getVisorsLen() > 0 {
		visors = api.getVisors()
	} else {
		visors, err = api.store.GetAllVisors(api.locDetails)
	}
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(visors); err != nil {
		api.writeError(w, r, err)
	}
}

func sendGone(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusGone)
}

func (api *API) handleUpdate() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		pk, ok := r.Context().Value(httpauth.ContextAuthKey).(cipher.PubKey)
		if !ok {
			api.writeError(w, r, errors.New("invalid auth"))
			return
		}

		visorIP := httpauth.GetRemoteAddr(r)
		// If the IP is private then we don't save it
		if !netutil.IsPublicIP(net.ParseIP(visorIP)) {
			visorIP = ""
		}

		query := r.URL.Query()
		visorVersion := query.Get("version")

		if err := api.store.UpdateUptime(pk.String(), visorIP, visorVersion); err != nil {
			api.writeError(w, r, err)
			return
		}

		w.WriteHeader(http.StatusOK)
	}
}

func (api *API) handleUptimes(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	pksStr := query.Get("visors")
	var pks []string
	if pksStr != "" {
		pks = strings.Split(pksStr, ",")
	}

	now := time.Now()
	// by default, we're fetching data for the current month
	startYear, startMonth, endYear, endMonth := now.Year(), now.Month(), now.Year(), now.Month()

	// here we take start date and end date to get time interval,
	// but day doesn't matter, we use interval from the month start of `startDate`
	// till the month end of `endDate`. but we still get complete timestamp from
	// the query parameters to simplify integration with the whitelisting system.
	startDateStr := query.Get("startDate")
	endDateStr := query.Get("endDate")
	// from what I see zeroes may happen here, guess they shouldn't be processed
	if startDateStr != "" && startDateStr != "0" && endDateStr != "" && endDateStr != "0" {
		startDateUnix, err := strconv.ParseInt(startDateStr, 10, 64)
		if err != nil {
			api.writeError(w, r, fmt.Errorf("invalid start date %s: %w", startDateStr, err))
			return
		}

		endDateUnix, err := strconv.ParseInt(endDateStr, 10, 64)
		if err != nil {
			api.writeError(w, r, fmt.Errorf("invalid end date %s: %w", endDateStr, err))
			return
		}

		startDate, endDate := time.Unix(startDateUnix, 0), time.Unix(endDateUnix, 0)

		startYear, startMonth, endYear, endMonth = startDate.Year(), startDate.Month(), endDate.Year(), endDate.Month()
	}

	var (
		uptimes store.UptimeResponse
		err     error
	)
	if len(pks) == 0 {
		if api.getAllUptimesLen() > 0 && isCurrentPeriod(startYear, startMonth) {
			uptimes = api.getAllUptimes()
		} else {
			uptimes, err = api.store.GetAllUptimes(startYear, startMonth, endYear, endMonth)
		}
	} else {
		if api.getAllUptimesLen() > 0 && isCurrentPeriod(startYear, startMonth) {
			allUptimes := api.getAllUptimes()
			uptimes = selectUptimesByPKs(allUptimes, pks)
		} else {
			uptimes, err = api.store.GetUptimes(pks, startYear, startMonth, endYear, endMonth)
		}
	}
	if err != nil {
		api.writeError(w, r, fmt.Errorf("failed to get uptimes: %w", err))
		return
	}

	if query.Get("status") == "on" {
		var tmpUptime store.UptimeResponse
		for _, uptime := range uptimes {
			if uptime.Online {
				tmpUptime = append(tmpUptime, uptime)
			}
		}
		uptimes = tmpUptime
	} else if query.Get("status") == "off" {
		var tmpUptime store.UptimeResponse
		for _, uptime := range uptimes {
			if !uptime.Online {
				tmpUptime = append(tmpUptime, uptime)
			}
		}
		uptimes = tmpUptime
	}

	if query.Get("v") == "v2" {
		dailyUptimeHistory := api.getDailyUptimes()
		var uptimesV2 store.UptimeResponseV2
		for _, uptime := range uptimes {
			var uptimev2 store.UptimeDefV2
			uptimev2.Key = uptime.Key
			uptimev2.Online = uptime.Online
			uptimev2.DailyOnlineHistory = dailyUptimeHistory[uptime.Key]
			uptimev2.Version = uptime.Version
			uptimesV2 = append(uptimesV2, uptimev2)
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(uptimesV2); err != nil {
			api.writeError(w, r, err)
		}
	} else {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(uptimes); err != nil {
			api.writeError(w, r, err)
		}
	}
}

func (api *API) handleUptime(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	pk, err := retrievePkFromURL(r.URL)
	if err != nil {
		api.writeError(w, r, err)
		return
	}

	var nilPK cipher.PubKey

	if pk == nilPK {
		api.writeError(w, r, fmt.Errorf("invalid public key"))
		return
	}

	pks := []string{pk.String()}

	now := time.Now()
	// by default, we're fetching data for the current month
	startYear, startMonth, endYear, endMonth := now.Year(), now.Month(), now.Year(), now.Month()

	// here we take start date and end date to get time interval,
	// but day doesn't matter, we use interval from the month start of `startDate`
	// till the month end of `endDate`. but we still get complete timestamp from
	// the query parameters to simplify integration with the whitelisting system.
	startDateStr := query.Get("startDate")
	endDateStr := query.Get("endDate")
	// from what I see zeroes may happen here, guess they shouldn't be processed
	if startDateStr != "" && startDateStr != "0" && endDateStr != "" && endDateStr != "0" {
		startDateUnix, err := strconv.ParseInt(startDateStr, 10, 64)
		if err != nil {
			api.writeError(w, r, fmt.Errorf("invalid start date %s: %w", startDateStr, err))
			return
		}

		endDateUnix, err := strconv.ParseInt(endDateStr, 10, 64)
		if err != nil {
			api.writeError(w, r, fmt.Errorf("invalid end date %s: %w", endDateStr, err))
			return
		}

		startDate, endDate := time.Unix(startDateUnix, 0), time.Unix(endDateUnix, 0)

		startYear, startMonth, endYear, endMonth = startDate.Year(), startDate.Month(), endDate.Year(), endDate.Month()
	}

	var (
		uptimes store.UptimeResponse
	)

	if api.getAllUptimesLen() > 0 && isCurrentPeriod(startYear, startMonth) {
		allUptimes := api.getAllUptimes()
		uptimes = selectUptimesByPKs(allUptimes, pks)
	} else {
		uptimes, err = api.store.GetUptimes(pks, startYear, startMonth, endYear, endMonth)
	}

	if err != nil {
		api.writeError(w, r, fmt.Errorf("failed to get uptimes: %w", err))
		return
	}

	if len(uptimes) == 0 {
		api.writeError(w, r, fmt.Errorf("no entries for pk %v", pk))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(uptimes[0]); err != nil {
		api.writeError(w, r, err)
	}
}

func (api *API) health(w http.ResponseWriter, r *http.Request) {
	info := buildinfo.Get()
	api.writeJSON(w, r, http.StatusOK, HealthCheckResponse{
		BuildInfo:   info,
		StartedAt:   api.startedAt,
		DmsgAddr:    api.dmsgAddr,
		DmsgServers: api.DmsgServers,
	})
}

func (api *API) writeJSON(w http.ResponseWriter, r *http.Request, code int, object interface{}) {
	jsonObject, err := json.Marshal(object)
	if err != nil {
		api.logger(r).WithError(err).Errorf("failed to encode json response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_, err = w.Write(jsonObject)
	if err != nil {
		api.logger(r).WithError(err).Errorf("failed to write json response")
	}
}

func (api *API) logger(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

func (api *API) chart(w http.ResponseWriter, r *http.Request) {
	chartLength := 6
	if query, ok := r.URL.Query()["length"]; ok {
		lengthValue, err := strconv.Atoi(query[0])
		if err == nil || lengthValue > 0 {
			chartLength = lengthValue
		}
	}

	bar := charts.NewBar()
	bar.SetGlobalOptions(
		charts.WithTitleOpts(opts.Title{
			Title:    "Skywire Nodes",
			Subtitle: "Node Counts",
		}),
		charts.WithInitializationOpts(opts.Initialization{
			Width:     "1200px",
			Height:    "600px",
			PageTitle: "Monthly Nodecount",
		}),
	)
	items, xAxis, err := api.generateData(chartLength)
	if err != nil {
		api.logger(r).WithError(err).Errorf("failed to generate data for bar graph")
	}

	bar.SetXAxis(xAxis).
		AddSeries("Months", items)

	err = bar.Render(w)
	if err != nil {
		api.logger(r).WithError(err).Errorf("failed to render bar graph")
	}
}

// generate data for bar chart
func (api *API) generateData(length int) ([]opts.BarData, []string, error) {
	now := time.Now()
	day := now.Day()
	// This is because how the time package handles the month subtraction in AddDate
	// eg. on Jun 30th the first two months will be march because Feb won't be in it since it has 28 days
	// So we take the month back to first date.
	subDay := day - 1
	firstOfMonth := now.AddDate(0, 0, -subDay)
	items := make([]opts.BarData, 0)
	var xAxis []string
	for i := length - 1; i >= 0; i-- {
		month := firstOfMonth.AddDate(0, -i, 0).Month()
		year := firstOfMonth.AddDate(0, -i, 0).Year()
		value, err := api.store.GetNumberOfUptimesByYearAndMonth(year, month)
		if err != nil {
			return nil, nil, err
		}
		xAxis = append(xAxis, month.String()+" "+fmt.Sprint(year))
		items = append(items, opts.BarData{Value: value})
	}
	return items, xAxis, nil
}

// updateInternalState is function which updates of total number of uptimes in current month
func (api *API) updateInternalState(_ context.Context, logger logrus.FieldLogger) {
	uptimesCount, err := api.store.GetNumberOfUptimesInCurrentMonth()
	if err != nil {
		logger.WithError(err).Errorf("failed to get number of uptimes in current month")
		return
	}

	api.metrics.SetEntriesCount(int64(uptimesCount))
}

func selectUptimesByPKs(allUptimes store.UptimeResponse, pks []string) store.UptimeResponse {
	uptimes := make(store.UptimeResponse, 0)
	for _, pk := range pks {
		for _, uptime := range allUptimes {
			if pk == uptime.Key {
				uptimes = append(uptimes, uptime)
			}
		}
	}
	return uptimes
}

func isCurrentPeriod(startYear int, startMonth time.Month) bool {
	now := time.Now()
	currentYear, currentMonth := now.Year(), now.Month()
	return startYear == currentYear && startMonth == currentMonth
}

// NewPrivate constructs a new PrivateAPI instance.
func NewPrivate(log logrus.FieldLogger, s store.Store) *PrivateAPI {
	if log == nil {
		log = logging.MustGetLogger("uptime_tracker")
	}

	pAPI := &PrivateAPI{
		store:     s,
		startedAt: time.Now(),
	}

	r := chi.NewRouter()

	r.Use(httputil.NewLogMiddleware(log))
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Get("/visor-ips", pAPI.handleVisorIPs)

	pAPI.Handler = r

	return pAPI
}

func (pApi *PrivateAPI) handleVisorIPs(w http.ResponseWriter, r *http.Request) {
	month := r.URL.Query().Get("month")
	if month == "" {
		month = "all"
	}

	ipMap, err := pApi.store.GetVisorsIPs(month)
	if err != nil {
		pApi.writeError(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ipMap); err != nil {
		pApi.writeError(w, r, err)
	}
}

func (pApi *PrivateAPI) log(r *http.Request) logrus.FieldLogger {
	return httputil.GetLogger(r)
}

// ServeHTTP implements http.Handler.
func (pApi *PrivateAPI) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var status int

	if err == context.DeadlineExceeded {
		status = http.StatusRequestTimeout
	}

	// we still haven't found the error
	if status == 0 {
		if _, ok := err.(*json.SyntaxError); ok {
			status = http.StatusBadRequest
		}
	}

	// we fallback to 500
	if status == 0 {
		status = http.StatusInternalServerError
	}

	if status != http.StatusNotFound {
		pApi.log(r).Warnf("%d: %s", status, err)
	}

	w.WriteHeader(status)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(&Error{Error: err.Error()}); err != nil {
		pApi.log(r).WithError(err).Warn("Failed to encode error")
	}
}

// retrievePkFromURL returns the id used on endpoints of the form path/:pk
// it doesn't checks if the endpoint has this form and can fail with other
// endpoint forms
func retrievePkFromURL(url *url.URL) (cipher.PubKey, error) {
	splitPath := strings.Split(url.EscapedPath(), "/")
	v := splitPath[len(splitPath)-1]
	pk := cipher.PubKey{}
	err := pk.UnmarshalText([]byte(v))
	return pk, err
}
