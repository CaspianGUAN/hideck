package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yibaiba/hideck/internal/config"
	"github.com/yibaiba/hideck/internal/db"
	"github.com/yibaiba/hideck/internal/device"
	"github.com/yibaiba/hideck/internal/mbn"
)

type offlineMBNGateway struct{}

func (offlineMBNGateway) Identity(context.Context, string) (mbn.Owner, error) {
	return mbn.Owner{DeviceID: "d", IMEI: "imei", ICCID: "card"}, nil
}
func (offlineMBNGateway) Open(context.Context, string, bool) (mbn.Session, error) {
	return nil, errors.New("test modem is offline")
}

func mbnTestRouter(t *testing.T) (*gin.Engine, *mbn.Service, string) {
	t.Helper()
	openTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	service := mbn.New(mbn.Options{Context: ctx, Store: mbn.NewDatabaseStore(db.DB), Files: mbn.DiskFiles{Directory: t.TempDir()}, Gateway: offlineMBNGateway{}})
	t.Cleanup(func() { cancel(); service.Wait() })
	p := &device.Pool{}
	p.SetMBNService(service)
	s := &Server{pool: p, auth: config.WebConfig{Username: "admin", Password: "secret"}}
	r := gin.New()
	group := r.Group("/api")
	group.Use(s.authMiddleware(), s.mbnMutationMiddleware())
	s.registerMBNRoutes(group)
	return r, service, testSessionToken(t, "secret", time.Now().Add(time.Hour))
}

func TestMBNFilesAuthenticatedRoundTrip(t *testing.T) {
	r, _, token := mbnTestRouter(t)
	response := performPhoneRequest(r, http.MethodGet, "/api/mbn/files", "", "", nil)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("%d", response.Code)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "profile.mbn")
	if err != nil {
		t.Fatal(err)
	}
	data := []byte{0, 0xff, 0x1b, 'a', 'b'}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/mbn/files", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	r.ServeHTTP(response, req)
	if response.Code != http.StatusCreated {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	var asset mbn.Asset
	if err := json.Unmarshal(response.Body.Bytes(), &asset); err != nil {
		t.Fatal(err)
	}
	response = performPhoneRequest(r, http.MethodGet, "/api/mbn/files/"+asset.ID, token, "", nil)
	if response.Code != http.StatusOK || !bytes.Equal(response.Body.Bytes(), data) {
		t.Fatalf("download %d %x", response.Code, response.Body.Bytes())
	}
	response = performPhoneRequest(r, http.MethodDelete, "/api/mbn/files/"+asset.ID, token, "", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("delete %d %s", response.Code, response.Body.String())
	}
}

func TestMBNOperationPublishesFailureNotFakeSuccess(t *testing.T) {
	r, service, token := mbnTestRouter(t)
	request := mbn.Request{RequestID: "test", Kind: "apply", Profile: "A", ExpectedOwner: &mbn.Owner{DeviceID: "d", IMEI: "imei", ICCID: "card"}}
	response := performPhoneRequest(r, http.MethodPost, "/api/devices/d/mbn/operations", token, "", request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	service.Wait()
	response = performPhoneRequest(r, http.MethodGet, "/api/devices/d/mbn/operations", token, "", nil)
	var body struct {
		Items []mbn.Operation `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].Status != "failed" || body.Items[0].Error != "test modem is offline" {
		t.Fatalf("%s", response.Body.String())
	}
}

func TestMBNRejectsMissingAndStaleOwnerAtHTTPBoundary(t *testing.T) {
	r, _, token := mbnTestRouter(t)
	request := mbn.Request{RequestID: "test", Kind: "apply", Profile: "A"}
	response := performPhoneRequest(r, http.MethodPost, "/api/devices/d/mbn/operations", token, "", request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	request.ExpectedOwner = &mbn.Owner{DeviceID: "d", IMEI: "imei", ICCID: "old-card"}
	response = performPhoneRequest(r, http.MethodPost, "/api/devices/d/mbn/operations", token, "", request)
	if response.Code != http.StatusConflict {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}
