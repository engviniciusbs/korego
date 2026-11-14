package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/engviniciusbs/korego/internal/store"
)

func lastMeta(t *testing.T, db *store.DB) map[string]any {
	t.Helper()
	rows, err := db.ListAudit(context.Background(), 1, 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(rows[0].Metadata), &m))
	return m
}

func TestRecord_MergesTokenMetadata(t *testing.T) {
	db := store.OpenMemory(t)
	l := &Logger{DB: db, Now: func() time.Time { return time.Unix(1_800_000_000, 0) }}

	orig := map[string]any{"app": "x"}
	ctx := WithToken(context.Background(), TokenRef{ID: 7, Name: "notebook"})
	require.NoError(t, l.Record(ctx, Entry{Action: "app.delete", Metadata: orig}))
	m := lastMeta(t, db)
	require.Equal(t, "token", m["via"])
	require.Equal(t, "notebook", m["token"])
	require.EqualValues(t, 7, m["tokenId"])
	require.Equal(t, "x", m["app"])
	require.Equal(t, map[string]any{"app": "x"}, orig)

	// Token without entry metadata.
	db2 := store.OpenMemory(t)
	l2 := &Logger{DB: db2}
	require.NoError(t, l2.Record(ctx, Entry{Action: "a"}))
	require.Equal(t, "token", lastMeta(t, db2)["via"])

	// No token: unchanged.
	db3 := store.OpenMemory(t)
	l3 := &Logger{DB: db3}
	require.NoError(t, l3.Record(context.Background(), Entry{Action: "a", Metadata: map[string]any{"k": "v"}}))
	require.Equal(t, map[string]any{"k": "v"}, lastMeta(t, db3))
}
