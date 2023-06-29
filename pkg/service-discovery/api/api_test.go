package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/internal/sdmetrics"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/service-discovery/store"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

// apiTestWant describes the desired result of the test case.
type apiTestWant struct {
	// code is an HTTP status code wanted.
	code int
	// body is a response body wanted.
	body interface{}
}

func TestAPI_GetServices(t *testing.T) {
	tests := []struct {
		// name is a test case name.
		name string
		// url is an URL to send request to.
		url string
		// sType is a service type of discovery to be included in the request if present.
		sType string
		// lat is latitude to be included in the request if present.
		lat string
		// lon is a longitude to be included in the request if present.
		lon string
		// method is an HTTP method to use with the request.
		method string
		// servicesRes is a bunch of services to be returned out of `Services` call to storage mock.
		servicesRes []servicedisc.Service
		// servicesErr is an error to be returned out of `Services` call to storage mock.
		servicesErr *servicedisc.HTTPError
		// want is a desired test case result.
		want apiTestWant
	}{
		{
			// common requests resulting in 200 status code with non-empty response body.
			name:   "ok",
			url:    "/api/services",
			sType:  "type",
			method: http.MethodGet,
			servicesRes: []servicedisc.Service{
				{
					Addr: servicedisc.SWAddr{},
					Type: "type1",
				},
				{
					Addr: servicedisc.SWAddr{},
					Type: "type2",
				},
			},
			want: apiTestWant{
				code: http.StatusOK,
			},
		},
		{
			// request with the non-allowed HTTP method.
			name:   "wrong HTTP method",
			url:    "/api/services",
			sType:  "type",
			method: http.MethodConnect,
			servicesRes: []servicedisc.Service{
				{
					Addr: servicedisc.SWAddr{},
					Type: "type1",
				},
				{
					Addr: servicedisc.SWAddr{},
					Type: "type2",
				},
			},
			want: apiTestWant{
				code: http.StatusMethodNotAllowed,
			},
		},
		{
			// missing `type` query parameter which results in 400 status code.
			name:   "missing type",
			url:    "/api/services",
			sType:  "",
			method: http.MethodGet,
			servicesRes: []servicedisc.Service{
				{
					Addr: servicedisc.SWAddr{},
					Type: "type1",
				},
				{
					Addr: servicedisc.SWAddr{},
					Type: "type2",
				},
			},
			want: apiTestWant{
				code: http.StatusBadRequest,
			},
		},
		{
			// internal error in the storage - results in 500 status code.
			name:        "internal storage error",
			url:         "/api/services",
			sType:       "type",
			method:      http.MethodGet,
			servicesRes: nil,
			servicesErr: &servicedisc.HTTPError{
				HTTPStatus: http.StatusInternalServerError,
				Err:        "database error",
			},
			want: apiTestWant{
				code: http.StatusInternalServerError,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &store.MockStore{}
			db.On("Services", mock.Anything, tc.sType).Return(tc.servicesRes, uint64(0), tc.servicesErr)
			m := sdmetrics.NewEmpty()
			api := New(logging.MustGetLogger("test_service-discovery"), db, nil, "", false, m, "")

			rr := httptest.NewRecorder()

			url := tc.url
			if tc.sType != "" {
				url += "?type=" + tc.sType
			}

			req, err := http.NewRequest(tc.method, url, nil)
			require.NoError(t, err)

			api.ServeHTTP(rr, req)

			require.Equal(t, tc.want.code, rr.Code)
			if rr.Code != http.StatusOK {
				return
			}

			var gotServices []servicedisc.Service
			err = json.Unmarshal(rr.Body.Bytes(), &gotServices)
			require.NoError(t, err)

			require.Equal(t, tc.servicesRes, gotServices)
		})
	}
}

func TestAPI_GetService(t *testing.T) {
	validPK, _ := cipher.GenerateKeyPair()
	validSWAddr := servicedisc.NewSWAddr(validPK, 10054)

	tests := []struct {
		// name is a test case name.
		name string
		// url is an URL to send request to.
		url string
		// sType is a service type of discovery to be included in the request if present.
		sType string
		// method is an HTTP method to use with the request.
		method string
		// serviceAddr is an address passed to `Service` call to storage mock.
		serviceAddr servicedisc.SWAddr
		// serviceAddrStr is a string representation of `servicesAddr` included in the query arg.
		serviceAddrStr string
		// serviceRes is a service to be returned out of `Service` call to storage mock.
		serviceRes *servicedisc.Service
		// serviceErr is an error to be returned out of `Service` call to storage mock.
		serviceErr *servicedisc.HTTPError
		// want is a desired test case result.
		want apiTestWant
	}{
		{
			// common requests resulting in 200 status code with non-empty response body.
			name:           "ok",
			url:            "/api/services/",
			sType:          "type",
			method:         http.MethodGet,
			serviceAddr:    validSWAddr,
			serviceAddrStr: validSWAddr.String(),
			serviceRes: &servicedisc.Service{
				Addr: servicedisc.SWAddr{},
				Type: "type",
			},
			want: apiTestWant{
				code: http.StatusOK,
			},
		},
		{
			// request with the non-allowed HTTP method.
			name:           "wrong HTTP method",
			url:            "/api/services/",
			sType:          "type",
			method:         http.MethodConnect,
			serviceAddr:    validSWAddr,
			serviceAddrStr: validSWAddr.String(),
			serviceRes: &servicedisc.Service{
				Addr: servicedisc.SWAddr{},
				Type: "type",
			},
			want: apiTestWant{
				code: http.StatusMethodNotAllowed,
			},
		},
		{
			// missing `type` query parameter which results in 400 status code.
			name:           "missing type",
			url:            "/api/services/",
			sType:          "",
			method:         http.MethodGet,
			serviceAddr:    validSWAddr,
			serviceAddrStr: validSWAddr.String(),
			serviceRes: &servicedisc.Service{
				Addr: servicedisc.SWAddr{},
				Type: "type",
			},
			want: apiTestWant{
				code: http.StatusBadRequest,
			},
		},
		{
			// request with the invalid SW addr query arg - results in 400 status code.
			name:           "invalid SW addr",
			url:            "/api/services/",
			sType:          "type",
			method:         http.MethodGet,
			serviceAddr:    servicedisc.NewSWAddr(cipher.PubKey{}, 0),
			serviceAddrStr: "invalid",
			serviceRes: &servicedisc.Service{
				Addr: servicedisc.SWAddr{},
				Type: "type",
			},
			want: apiTestWant{
				code: http.StatusBadRequest,
			},
		},
		{
			// internal error in the storage - results in 500 status code.
			name:           "internal repository error",
			url:            "/api/services/",
			sType:          "type",
			method:         http.MethodGet,
			serviceAddr:    validSWAddr,
			serviceAddrStr: validSWAddr.String(),
			serviceRes:     nil,
			serviceErr: &servicedisc.HTTPError{
				HTTPStatus: http.StatusInternalServerError,
				Err:        "database error",
			},
			want: apiTestWant{
				code: http.StatusInternalServerError,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &store.MockStore{}
			db.On("Service", mock.Anything, tc.sType, tc.serviceAddr).Return(tc.serviceRes, tc.serviceErr)
			m := sdmetrics.NewEmpty()
			api := New(logging.MustGetLogger("test_service-discovery"), db, nil, "", false, m, "")

			rr := httptest.NewRecorder()

			url := tc.url + tc.serviceAddrStr
			if tc.sType != "" {
				url += "?type=" + tc.sType
			}

			req, err := http.NewRequest(tc.method, url, nil)
			require.NoError(t, err)

			api.ServeHTTP(rr, req)

			require.Equal(t, tc.want.code, rr.Code)
			if rr.Code != http.StatusOK {
				return
			}

			var gotService servicedisc.Service
			err = json.Unmarshal(rr.Body.Bytes(), &gotService)
			require.NoError(t, err)

			require.Equal(t, *tc.serviceRes, gotService)
		})
	}
}

func TestAPI_UpdateService(t *testing.T) {
	service := servicedisc.Service{
		Addr: servicedisc.SWAddr{},
		Type: "type",
	}

	serviceBytes, err := json.Marshal(&service)
	require.NoError(t, err)

	tests := []struct {
		// name is a test case name.
		name string
		// url is an URL to send request to.
		url string
		// sType is a service type of discovery to be included in the request if present.
		sType string
		// method is an HTTP method to use with the request.
		method string
		// service is a service to be sent as a request body.
		service servicedisc.Service
		// serviceBytes is a JSON-marshaled `service`.
		serviceBytes []byte
		// updateServiceErr is an error to be returned out of `UpdateService` call to storage mock.
		updateServiceErr *servicedisc.HTTPError
		// want is a desired test case result.
		want apiTestWant
	}{
		{
			// common requests resulting in 200 status code with non-empty response body.
			name:         "ok",
			url:          "/api/services",
			sType:        "type",
			method:       http.MethodPost,
			service:      service,
			serviceBytes: serviceBytes,
			want: apiTestWant{
				code: http.StatusOK,
				body: service,
			},
		},
		{
			// request with the non-allowed HTTP method.
			name:         "wrong HTTP method",
			url:          "/api/services",
			sType:        "type",
			method:       http.MethodConnect,
			service:      service,
			serviceBytes: serviceBytes,
			want: apiTestWant{
				code: http.StatusMethodNotAllowed,
			},
		},
		{
			// request with the malformed JSON body - results in 400 status code.
			name:         "invalid request body",
			url:          "/api/services",
			sType:        "type",
			method:       http.MethodPost,
			service:      service,
			serviceBytes: nil,
			want: apiTestWant{
				code: http.StatusBadRequest,
			},
		},
		{
			// internal error in the storage - results in 500 status code.
			name:         "internal repository error",
			url:          "/api/services",
			sType:        "type",
			method:       http.MethodPost,
			service:      service,
			serviceBytes: serviceBytes,
			updateServiceErr: &servicedisc.HTTPError{
				HTTPStatus: http.StatusInternalServerError,
				Err:        "database error",
			},
			want: apiTestWant{
				code: http.StatusInternalServerError,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &store.MockStore{}
			db.On("UpdateService", mock.Anything, &tc.service).Return(tc.updateServiceErr)
			m := sdmetrics.NewEmpty()
			api := New(logging.MustGetLogger("test_service-discovery"), db, nil, "", false, m, "")

			rr := httptest.NewRecorder()

			url := tc.url

			req, err := http.NewRequest(tc.method, url, bytes.NewReader(tc.serviceBytes))
			require.NoError(t, err)

			api.ServeHTTP(rr, req)

			require.Equal(t, tc.want.code, rr.Code)
			if rr.Code != http.StatusOK {
				return
			}

			var gotService servicedisc.Service
			err = json.Unmarshal(rr.Body.Bytes(), &gotService)
			require.NoError(t, err)

			require.Equal(t, tc.service, gotService)
		})
	}
}

func TestAPI_DelService(t *testing.T) {
	validPK, _ := cipher.GenerateKeyPair()
	validSWAddr := servicedisc.NewSWAddr(validPK, 10054)

	tests := []struct {
		// name is a test case name.
		name string
		// url is an URL to send request to.
		url string
		// sType is a service type of discovery to be included in the request if present.
		sType string
		// method is an HTTP method to use with the request.
		method string
		// serviceAddr is an address to be passed to the `DeleteService` call to the storage mock.
		serviceAddr servicedisc.SWAddr
		// serviceAddrStr is a string representation of `serviceAddr` to be passed in the query arg.
		serviceAddrStr string
		// deleteServiceErr is an error to be returned out of `DeleteService` call to the storage mock.
		deleteServiceErr *servicedisc.HTTPError
		// want is a desired test case result.
		want apiTestWant
	}{
		{
			// common requests resulting in 200 status code.
			name:           "ok",
			url:            "/api/services/",
			sType:          "type",
			method:         http.MethodDelete,
			serviceAddr:    validSWAddr,
			serviceAddrStr: validSWAddr.String(),
			want: apiTestWant{
				code: http.StatusOK,
			},
		},
		{
			// request with the non-allowed HTTP method.
			name:           "wrong HTTP method",
			url:            "/api/services/",
			sType:          "type",
			method:         http.MethodConnect,
			serviceAddr:    validSWAddr,
			serviceAddrStr: validSWAddr.String(),
			want: apiTestWant{
				code: http.StatusMethodNotAllowed,
			},
		},
		{
			// missing `type` query parameter which results in 400 status code.
			name:           "missing type",
			url:            "/api/services/",
			sType:          "",
			method:         http.MethodDelete,
			serviceAddr:    validSWAddr,
			serviceAddrStr: validSWAddr.String(),
			want: apiTestWant{
				code: http.StatusBadRequest,
			},
		},
		{
			// request with the invalid SW addr query arg - results in 400 status code.
			name:           "invalid SW addr",
			url:            "/api/services/",
			sType:          "type",
			method:         http.MethodDelete,
			serviceAddr:    validSWAddr,
			serviceAddrStr: "invalid",
			want: apiTestWant{
				code: http.StatusBadRequest,
			},
		},
		{
			// internal error in the storage - results in 500 status code.
			name:           "internal repository error",
			url:            "/api/services/",
			sType:          "type",
			method:         http.MethodDelete,
			serviceAddr:    validSWAddr,
			serviceAddrStr: validSWAddr.String(),
			deleteServiceErr: &servicedisc.HTTPError{
				HTTPStatus: http.StatusInternalServerError,
				Err:        "database error",
			},
			want: apiTestWant{
				code: http.StatusInternalServerError,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &store.MockStore{}
			db.On("DeleteService", mock.Anything, tc.sType, tc.serviceAddr).Return(tc.deleteServiceErr)
			m := sdmetrics.NewEmpty()
			api := New(logging.MustGetLogger("test_service-discovery"), db, nil, "", false, m, "")

			rr := httptest.NewRecorder()

			url := tc.url + tc.serviceAddrStr
			if tc.sType != "" {
				url += "?type=" + tc.sType
			}

			req, err := http.NewRequest(tc.method, url, nil)
			require.NoError(t, err)

			api.ServeHTTP(rr, req)

			require.Equal(t, tc.want.code, rr.Code)
		})
	}
}

func TestAPI_AddVPNFromOldVisor(t *testing.T) {
	service := servicedisc.Service{
		Addr:    servicedisc.SWAddr{},
		Type:    servicedisc.ServiceTypeVPN,
		Version: "",
	}

	serviceBytes, err := json.Marshal(&service)
	require.NoError(t, err)

	db := &store.MockStore{}
	// db.On("UpdateService", mock.Anything, &tc.service).Return(nil)
	m := sdmetrics.NewEmpty()
	api := New(logging.MustGetLogger("test_service-discovery"), db, nil, "", false, m, "")

	rr := httptest.NewRecorder()

	req, err := http.NewRequest(http.MethodPost, "/api/services", bytes.NewReader(serviceBytes))
	require.NoError(t, err)

	api.ServeHTTP(rr, req)

	res := &servicedisc.HTTPError{
		HTTPStatus: http.StatusForbidden,
		Err:        ErrVisorVersionIsTooOld.Error(),
	}

	require.Equal(t, res.HTTPStatus, rr.Code)

	var response servicedisc.HTTPError
	err = json.Unmarshal(rr.Body.Bytes(), &response)
	require.NoError(t, err)

	require.Equal(t, res.Error(), response.Err)
}

func TestAPI_AddVPNFromCurrentVisor(t *testing.T) {
	service := servicedisc.Service{
		Addr:    servicedisc.SWAddr{},
		Type:    servicedisc.ServiceTypeVPN,
		Version: "4.0.0",
	}

	serviceBytes, err := json.Marshal(&service)
	require.NoError(t, err)

	db := &store.MockStore{}
	db.On("UpdateService", mock.Anything, &service).Return(nil)

	m := sdmetrics.NewEmpty()
	api := New(logging.MustGetLogger("test_service-discovery"), db, nil, "", false, m, "")

	rr := httptest.NewRecorder()

	req, err := http.NewRequest(http.MethodPost, "/api/services", bytes.NewReader(serviceBytes))
	require.NoError(t, err)

	api.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
}
