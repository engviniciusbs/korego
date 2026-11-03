package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/api"
	"github.com/engviniciusbs/korego/internal/update"
)

type fakeUpdater struct {
	mu                    sync.Mutex
	current               string
	latest                string
	checkErr, applyErr    error
	applies, finishes     int
	finishErr, restoreErr error
	restores              int
	entered, release      chan struct{} // opcionais: seguram Apply
}

func (f *fakeUpdater) Current() string { return f.current }
func (f *fakeUpdater) Check(context.Context) (update.Release, error) {
	return update.Release{Version: f.latest}, f.checkErr
}
func (f *fakeUpdater) Apply(context.Context, update.Release) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applies++
	entered, release := f.entered, f.release
	f.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
		<-release
	}
	f.mu.Lock()
	return f.applyErr
}
func (f *fakeUpdater) StartFinish() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishes++
	return f.finishErr
}
func (f *fakeUpdater) Restore() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restores++
	return f.restoreErr
}

func updateEnv(t *testing.T, u *fakeUpdater) (*env, *http.Cookie, string) {
	t.Helper()
	e := newEnvDeps(t, api.Deps{Updater: u})
	_, cookie, csrf := e.setup(t)
	return e, cookie, csrf
}

func TestUpdateAPI_NoUpdate409(t *testing.T) {
	u := &fakeUpdater{current: "0.2.0", latest: "v0.2.0"}
	e, cookie, csrf := updateEnv(t, u)

	w := e.do(t, "GET", "/api/update", nil, withCookie(cookie))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, false, decode(t, w)["available"])

	w = e.do(t, "POST", "/api/update", nil, withCookie(cookie), withHeader("X-CSRF-Token", csrf))
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "NO_UPDATE", decode(t, w)["error"].(map[string]any)["code"])
	assert.Equal(t, 0, u.applies)
}

func TestUpdateAPI_CheckErrorIs200(t *testing.T) {
	u := &fakeUpdater{current: "0.1.0", checkErr: errors.New("sem rede")}
	e, cookie, _ := updateEnv(t, u)
	w := e.do(t, "GET", "/api/update", nil, withCookie(cookie))
	require.Equal(t, http.StatusOK, w.Code)
	b := decode(t, w)
	assert.Nil(t, b["latest"])
	assert.Equal(t, false, b["available"])
	assert.Equal(t, "sem rede", b["error"])
}

func TestUpdateAPI_AppliesAndAudits(t *testing.T) {
	u := &fakeUpdater{current: "0.1.0", latest: "v0.2.0"}
	e, cookie, csrf := updateEnv(t, u)

	b := decode(t, e.do(t, "GET", "/api/update", nil, withCookie(cookie)))
	assert.Equal(t, true, b["available"])
	assert.Equal(t, "v0.2.0", b["latest"])

	w := e.do(t, "POST", "/api/update", nil, withCookie(cookie), withHeader("X-CSRF-Token", csrf))
	require.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, 1, u.applies)
	assert.Equal(t, 1, u.finishes)

	rows, err := e.svc.DB.ListAudit(context.Background(), 20, 0)
	require.NoError(t, err)
	found := false
	for _, r := range rows {
		if r.Action == "system.update" {
			found = true
			assert.Contains(t, r.Metadata, "v0.2.0")
		}
	}
	assert.True(t, found)
}

func TestUpdateAPI_ApplyFailure502(t *testing.T) {
	u := &fakeUpdater{current: "0.1.0", latest: "v0.2.0", applyErr: errors.New("checksum diverge")}
	e, cookie, csrf := updateEnv(t, u)
	w := e.do(t, "POST", "/api/update", nil, withCookie(cookie), withHeader("X-CSRF-Token", csrf))
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Equal(t, "UPDATE_FAILED", decode(t, w)["error"].(map[string]any)["code"])
	assert.Equal(t, 0, u.finishes)
}

func TestUpdateAPI_InProgressGuard(t *testing.T) {
	u := &fakeUpdater{
		current: "0.1.0", latest: "v0.2.0",
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	e, cookie, csrf := updateEnv(t, u)
	post := func() *httptest.ResponseRecorder {
		return e.do(t, "POST", "/api/update", nil, withCookie(cookie), withHeader("X-CSRF-Token", csrf))
	}

	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- post() }()
	<-u.entered // primeiro POST está dentro de Apply

	w := post() // concorrente
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "UPDATE_IN_PROGRESS", decode(t, w)["error"].(map[string]any)["code"])

	close(u.release)
	assert.Equal(t, http.StatusAccepted, (<-first).Code)

	w = post() // sequencial, após 202
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "UPDATE_IN_PROGRESS", decode(t, w)["error"].(map[string]any)["code"])
	assert.Equal(t, 1, u.applies)
	assert.Equal(t, 1, u.finishes)
}

func TestUpdateAPI_FinishFailureRestoresAndRetries(t *testing.T) {
	u := &fakeUpdater{current: "0.1.0", latest: "v0.2.0", finishErr: errors.New("systemd-run falhou")}
	e, cookie, csrf := updateEnv(t, u)
	post := func() *httptest.ResponseRecorder {
		return e.do(t, "POST", "/api/update", nil, withCookie(cookie), withHeader("X-CSRF-Token", csrf))
	}
	w := post()
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Equal(t, "UPDATE_FAILED", decode(t, w)["error"].(map[string]any)["code"])
	assert.Equal(t, 1, u.restores)

	u.mu.Lock()
	u.finishErr = nil
	u.mu.Unlock()
	require.Equal(t, http.StatusAccepted, post().Code)
	assert.Equal(t, 2, u.applies)
}

func TestUpdateAPI_FinishAndRestoreFailureBlocks(t *testing.T) {
	u := &fakeUpdater{
		current: "0.1.0", latest: "v0.2.0",
		finishErr: errors.New("systemd-run falhou"), restoreErr: errors.New("sem .prev"),
	}
	e, cookie, csrf := updateEnv(t, u)
	post := func() *httptest.ResponseRecorder {
		return e.do(t, "POST", "/api/update", nil, withCookie(cookie), withHeader("X-CSRF-Token", csrf))
	}
	w := post()
	require.Equal(t, http.StatusBadGateway, w.Code)
	assert.Contains(t, w.Body.String(), "intervenção manual necessária")

	w = post()
	require.Equal(t, http.StatusConflict, w.Code)
	assert.Equal(t, "UPDATE_IN_PROGRESS", decode(t, w)["error"].(map[string]any)["code"])
	assert.Equal(t, 1, u.applies)
}
