package deploy

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/engviniciusbs/korego/internal/node/docker"
	"github.com/engviniciusbs/korego/internal/node/docker/dockertest"
	"github.com/engviniciusbs/korego/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func appFor(slug string) store.App { return store.App{Slug: slug, InternalPort: 8080} }

func fastPoll(t *testing.T) {
	old := healthInterval
	healthInterval = 2 * time.Millisecond
	t.Cleanup(func() { healthInterval = old })
}

// inspectSeq devolve os Details em sequência; o último se repete.
func inspectSeq(ds ...docker.Details) *dockertest.Fake {
	var n int32
	return &dockertest.Fake{InspectFn: func(context.Context, string) (docker.Details, error) {
		i := int(atomic.AddInt32(&n, 1)) - 1
		if i >= len(ds) {
			i = len(ds) - 1
		}
		return ds[i], nil
	}}
}

func det(state, health string) docker.Details {
	return docker.Details{Container: docker.Container{State: state, Health: health}}
}

func TestStartContainer_SpecExact(t *testing.T) {
	var got docker.RunSpec
	d := &dockertest.Fake{RunFn: func(_ context.Context, s docker.RunSpec) (string, error) {
		got = s
		return "cid1", nil
	}}
	env := []string{"A=1", "SECRET=x"}
	id, err := StartContainer(context.Background(), d, appFor("blog"), 42, "korego/blog:abc1234", env, 20007)
	require.NoError(t, err)
	assert.Equal(t, "cid1", id)
	assert.Equal(t, docker.RunSpec{
		Name:  "korego-blog-abc1234-d42",
		Image: "korego/blog:abc1234",
		Labels: map[string]string{
			"korego.managed": "true", "korego.app": "blog", "korego.deploy": "42",
		},
		Env:           env,
		Ports:         []docker.PortMapping{{HostIP: "127.0.0.1", HostPort: 20007, ContainerPort: 8080, Proto: "tcp"}},
		RestartPolicy: "unless-stopped",
	}, got)
}

func TestStartContainer_Validates(t *testing.T) {
	d := &dockertest.Fake{RunFn: func(context.Context, docker.RunSpec) (string, error) { return "x", nil }}
	_, err := StartContainer(context.Background(), d, appFor("Bad Slug"), 1, "korego/blog:abc", nil, 20000)
	assert.Error(t, err)
	_, err = StartContainer(context.Background(), d, appFor("blog"), 1, "korego/blog:abc", nil, 0)
	assert.Error(t, err)
}

func TestWaitHealthy_UsesHealthcheck(t *testing.T) {
	fastPoll(t)
	d := inspectSeq(det("running", "starting"), det("running", "starting"), det("running", "healthy"))
	err := WaitHealthy(context.Background(), d, "c", 20000, time.Second, func(string) error {
		t.Error("não deve usar dial com healthcheck")
		return nil
	})
	assert.NoError(t, err)
}

func TestWaitHealthy_Unhealthy(t *testing.T) {
	fastPoll(t)
	d := inspectSeq(det("running", "starting"), det("running", "unhealthy"))
	err := WaitHealthy(context.Background(), d, "c", 20000, time.Second, nil)
	assert.ErrorIs(t, err, ErrUnhealthy)
	assert.Contains(t, err.Error(), "unhealthy")
}

func TestWaitHealthy_TCPFallback(t *testing.T) {
	fastPoll(t)
	d := inspectSeq(det("running", ""))
	var calls int
	var addr string
	err := WaitHealthy(context.Background(), d, "c", 20003, time.Second, func(a string) error {
		calls++
		addr = a
		if calls <= 2 {
			return errors.New("recusado")
		}
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 3, calls)
	assert.Equal(t, "127.0.0.1:20003", addr)
}

func TestWaitHealthy_ExitedFailsFast(t *testing.T) {
	fastPoll(t)
	for _, state := range []string{"exited", "dead"} {
		dd := det(state, "")
		dd.ExitCode = 137
		start := time.Now()
		err := WaitHealthy(context.Background(), inspectSeq(dd), "c", 20000, 10*time.Second, func(string) error { return nil })
		assert.ErrorIs(t, err, ErrContainerExited)
		assert.Contains(t, err.Error(), "137")
		assert.Less(t, time.Since(start), time.Second)
	}
}

func TestWaitHealthy_Timeout(t *testing.T) {
	fastPoll(t)
	d := inspectSeq(det("running", "starting"))
	err := WaitHealthy(context.Background(), d, "c", 20000, 30*time.Millisecond, nil)
	assert.ErrorIs(t, err, ErrHealthTimeout)
}

func TestWaitHealthy_ContextCanceled(t *testing.T) {
	fastPoll(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := WaitHealthy(ctx, inspectSeq(det("running", "starting")), "c", 20000, time.Second, nil)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestWaitHealthy_InspectError(t *testing.T) {
	fastPoll(t)
	d := &dockertest.Fake{InspectFn: func(context.Context, string) (docker.Details, error) {
		return docker.Details{}, docker.ErrNotFound
	}}
	err := WaitHealthy(context.Background(), d, "c", 20000, time.Second, nil)
	assert.ErrorIs(t, err, docker.ErrNotFound)
}
