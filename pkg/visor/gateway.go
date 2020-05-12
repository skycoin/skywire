package visor

/*
	!!! DO NOT DELETE !!!
	TODO(evanlinjin): This is taking far too long, we will leave this to be completed later.
*/

//// App constants.
//const (
//	statusStop = iota
//	statusStart
//)
//
//type Gateway struct {
//	v *Visor
//}
//
///*
//	<<< VISOR ENDPOINTS >>>
//*/
//
//func handleGetHealth(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		hi := HealthInfo{
//			TransportDiscovery: http.StatusOK,
//			RouteFinder:        http.StatusOK,
//			SetupNode:          http.StatusOK,
//		}
//		if _, err := v.conf.TransportDiscovery(); err != nil {
//			hi.TransportDiscovery = http.StatusNotFound
//		}
//		if v.conf.Routing.RouteFinder == "" {
//			hi.RouteFinder = http.StatusNotFound
//		}
//		if len(v.conf.Routing.SetupNodes) == 0 {
//			hi.SetupNode = http.StatusNotFound
//		}
//		httputil.WriteJSON(w, r, http.StatusOK, hi)
//	}
//}
//
//func handleGetUptime(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		uptime := time.Since(v.startedAt).Seconds()
//		httputil.WriteJSON(w, r, http.StatusOK, uptime)
//	}
//}
//
//func handleGetSummary(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		httputil.WriteJSON(w, r, http.StatusOK, makeVisorSummary(v))
//	}
//}
//
///*
//	<<< APP ENDPOINTS >>>
//*/
//
//func handleGetApps(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		httputil.WriteJSON(w, r, http.StatusOK, v.Apps())
//	}
//}
//
//func handleGetApp(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		appState, ok := httpAppState(v, w, r)
//		if !ok {
//			return
//		}
//		httputil.WriteJSON(w, r, http.StatusOK, appState)
//	}
//}
//
//// TODO: simplify
//// nolint: funlen,gocognit,godox
//func handlePutApp(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		appS, ok := httpAppState(v, w, r)
//		if !ok {
//			return
//		}
//		var reqBody struct {
//			AutoStart *bool          `json:"autostart,omitempty"`
//			Status    *int           `json:"status,omitempty"`
//			Passcode  *string        `json:"passcode,omitempty"`
//			PK        *cipher.PubKey `json:"pk,omitempty"`
//		}
//		if err := httputil.ReadJSON(r, &reqBody); err != nil {
//			if err != io.EOF {
//				log.Warnf("handlePutApp request: %v", err)
//			}
//			httputil.WriteJSON(w, r, http.StatusBadRequest,
//				fmt.Errorf("failed to read JSON from http request body: %v", err))
//			return
//		}
//
//		if reqBody.AutoStart != nil {
//			if *reqBody.AutoStart != appS.AutoStart {
//				if err := v.setAutoStart(appS.AppName, *reqBody.AutoStart); err != nil {
//					httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
//					return
//				}
//			}
//		}
//
//		if reqBody.Status != nil {
//			switch *reqBody.Status {
//			case statusStop:
//				if err := v.StopApp(appS.AppName); err != nil {
//					httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
//					return
//				}
//			case statusStart:
//				if err := v.StartApp(appS.AppName); err != nil {
//					httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
//					return
//				}
//			default:
//				errMsg := fmt.Errorf("value of 'status' field is %d when expecting 0 or 1", *reqBody.Status)
//				httputil.WriteJSON(w, r, http.StatusBadRequest, errMsg)
//				return
//			}
//		}
//
//		const (
//			skysocksName       = "skysocks"
//			skysocksClientName = "skysocks-client"
//		)
//
//		if reqBody.Passcode != nil && appS.AppName == skysocksName {
//			if err := v.setSocksPassword(*reqBody.Passcode); err != nil {
//				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
//				return
//			}
//		}
//
//		if reqBody.PK != nil && appS.AppName == skysocksClientName {
//			if err := v.setSocksClientPK(*reqBody.PK); err != nil {
//				httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
//				return
//			}
//		}
//
//		appS, _ = v.App(appS.AppName)
//		httputil.WriteJSON(w, r, http.StatusOK, appS)
//	}
//}
//
//// AppLogsResp parses logs as json, along with the last obtained timestamp for use on subsequent requests
//type AppLogsResp struct {
//	LastLogTimestamp string   `json:"last_log_timestamp"`
//	Logs             []string `json:"logs"`
//}
//
//func handleGetAppLogsSince(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		appS, ok := httpAppState(v, w, r)
//		if !ok {
//			return
//		}
//
//		since := r.URL.Query().Get("since")
//		since = strings.Replace(since, " ", "+", 1) // we need to put '+' again that was replaced in the query string
//
//		// if time is not parsable or empty default to return all logs
//		t, err := time.Parse(time.RFC3339Nano, since)
//		if err != nil {
//			t = time.Unix(0, 0)
//		}
//
//		ls, err := app.NewLogStore(filepath.Join(v.dir(), appS.AppName), appS.AppName, "bbolt")
//		if err != nil {
//			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
//			return
//		}
//		logs, err := ls.LogsSince(t)
//		if err != nil {
//			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
//			return
//		}
//		if len(logs) == 0 {
//			httputil.WriteJSON(w, r, http.StatusServiceUnavailable, err)
//			return
//		}
//		httputil.WriteJSON(w, r, http.StatusOK, &AppLogsResp{
//			LastLogTimestamp: app.TimestampFromLog(logs[len(logs)-1]),
//			Logs:             logs,
//		})
//	}
//}
//
///*
//	<<< TRANSPORT ENDPOINTS >>>
//*/
//
//func handleTransportTypes(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		httputil.WriteJSON(w, r, http.StatusOK, v.tpM.Networks())
//	}
//}
//
//func handleGetTransport(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		tp, ok := httpTransport(v, w, r)
//		if !ok {
//			return
//		}
//		httputil.WriteJSON(w, r, http.StatusOK,
//			newTransportSummary(v.tpM, tp, true, v.router.SetupIsTrusted(tp.Remote())))
//	}
//}
//
//func handleGetTransports(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		qTypes := strSliceFromQuery(r, "type", nil)
//
//		qPKs, err := pkSliceFromQuery(r, "pk", nil)
//		if err != nil {
//			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
//			return
//		}
//
//		qLogs, err := httputil.BoolFromQuery(r, "logs", true)
//		if err != nil {
//			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
//			return
//		}
//
//		tps, err := listTransports(v, TransportsIn{
//			FilterTypes:qTypes,
//			FilterPubKeys:qPKs,
//			ShowLogs:qLogs,
//		})
//		if err != nil {
//			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
//			return
//		}
//		httputil.WriteJSON(w, r, http.StatusOK, tps)
//	}
//}
//
//type PostTransportReq struct {
//	TpType string        `json:"transport_type"`
//	Remote cipher.PubKey `json:"remote_pk"`
//	Public bool          `json:"public"`
//}
//
//func handlePostTransport(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		var reqB PostTransportReq
//		if err := httputil.ReadJSON(r, &reqB); err != nil {
//			if err != io.EOF {
//				log.Warnf("handlePostTransport request: %v", err)
//			}
//			httputil.WriteJSON(w, r, http.StatusBadRequest,
//				fmt.Errorf("failed to read JSON from http request body: %v", err))
//			return
//		}
//		mTp, err := v.tpM.SaveTransport(r.Context(), reqB.Remote, reqB.TpType)
//		if err != nil {
//			httputil.WriteJSON(w, r, http.StatusInternalServerError, err)
//			return
//		}
//		httputil.WriteJSON(w, r, http.StatusOK,
//			newTransportSummary(v.tpM, mTp, false, v.router.SetupIsTrusted(mTp.Remote())))
//	}
//}
//
//func handleDelTransport(v *Visor) http.HandlerFunc {
//	return func(w http.ResponseWriter, r *http.Request) {
//		tp, ok := httpTransport(v, w, r)
//		if !ok {
//			return
//		}
//		v.tpM.DeleteTransport(tp.Entry.ID)
//	}
//}
//
///*
//	<<< ROUTER ENDPOINTS >>>
//*/
//
//
///*
//	<<< HELPER FUNCTIONS >>>
//*/
//
//func httpAppState(v *Visor, w http.ResponseWriter, r *http.Request) (*AppState, bool) {
//	appName := chi.URLParam(r, "app")
//
//	appState, ok := v.App(appName)
//	if !ok {
//		httputil.WriteJSON(w, r, http.StatusNotFound,
//			fmt.Sprintf("app of name %s is not found in visor", appName))
//		return nil, false
//	}
//	return appState, true
//}
//
//func httpTransport(v *Visor, w http.ResponseWriter, r *http.Request) (*transport.ManagedTransport, bool) {
//	tid, err := uuidFromParam(r, "tid")
//	if err != nil {
//		httputil.WriteJSON(w, r, http.StatusBadRequest, err)
//		return nil, false
//	}
//	tp := v.tpM.Transport(tid)
//	if tp == nil {
//		httputil.WriteJSON(w, r, http.StatusNotFound,
//			fmt.Errorf("transport of ID %v is not found", tid))
//		return nil, false
//	}
//	return tp, true
//}
//
//func httpRoute(v *Visor, w http.ResponseWriter, r *http.Request) (routing.RouteID, bool) {
//	rid, err := ridFromParam(r, "rid")
//	if err != nil {
//		httputil.WriteJSON(w, r, http.StatusBadRequest, err)
//		return rid, false
//	}
//	return rid, true
//}
//
//func makeVisorSummary(v *Visor) *Summary {
//	var tpSums []*TransportSummary
//	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
//		isSetup := v.router.SetupIsTrusted(tp.Remote())
//		tpSums = append(tpSums, newTransportSummary(v.tpM, tp, true, isSetup))
//		return true
//	})
//	return &Summary{
//		PubKey:          v.conf.Visor.PubKey,
//		BuildInfo:       buildinfo.Get(),
//		AppProtoVersion: supportedProtocolVersion,
//		Apps:            v.Apps(),
//		Transports:      tpSums,
//		RoutesCount:     v.rt.Count(),
//	}
//}
//
//func uuidFromParam(r *http.Request, key string) (uuid.UUID, error) {
//	return uuid.Parse(chi.URLParam(r, key))
//}
//
//func ridFromParam(r *http.Request, key string) (routing.RouteID, error) {
//	rid, err := strconv.ParseUint(chi.URLParam(r, key), 10, 32)
//	if err != nil {
//		return 0, errors.New("invalid route ID provided")
//	}
//
//	return routing.RouteID(rid), nil
//}
//
//func strSliceFromQuery(r *http.Request, key string, defaultVal []string) []string {
//	slice, ok := r.URL.Query()[key]
//	if !ok {
//		return defaultVal
//	}
//
//	return slice
//}
//
//func pkSliceFromQuery(r *http.Request, key string, defaultVal []cipher.PubKey) ([]cipher.PubKey, error) {
//	qPKs, ok := r.URL.Query()[key]
//	if !ok {
//		return defaultVal, nil
//	}
//
//	pks := make([]cipher.PubKey, len(qPKs))
//
//	for i, qPK := range qPKs {
//		pk := cipher.PubKey{}
//		if err := pk.UnmarshalText([]byte(qPK)); err != nil {
//			return nil, err
//		}
//
//		pks[i] = pk
//	}
//	return pks, nil
//}
//
//func listTransports(v *Visor, in TransportsIn) ([]*TransportSummary, error) {
//	typeIncluded := func(tType string) bool {
//		if in.FilterTypes != nil {
//			for _, ft := range in.FilterTypes {
//				if tType == ft {
//					return true
//				}
//			}
//			return false
//		}
//		return true
//	}
//	pkIncluded := func(localPK, remotePK cipher.PubKey) bool {
//		if in.FilterPubKeys != nil {
//			for _, fpk := range in.FilterPubKeys {
//				if localPK == fpk || remotePK == fpk {
//					return true
//				}
//			}
//			return false
//		}
//		return true
//	}
//	var tps []*TransportSummary
//	v.tpM.WalkTransports(func(tp *transport.ManagedTransport) bool {
//		if typeIncluded(tp.Type()) && pkIncluded(v.tpM.Local(), tp.Remote()) {
//			tps = append(tps, newTransportSummary(v.tpM, tp, in.ShowLogs, v.router.SetupIsTrusted(tp.Remote())))
//		}
//		return true
//	})
//	return tps, nil
//}
