package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestAccountUsageSortExpressionUsesWindowTokenTotals(t *testing.T) {
	for _, tc := range []struct {
		sortBy        string
		resetAtKey    string
		resetAfterKey string
		interval      string
	}{
		{sortBy: "usage_5h", resetAtKey: "codex_5h_reset_at", resetAfterKey: "codex_5h_reset_after_seconds", interval: "5 hours"},
		{sortBy: "usage_7d", resetAtKey: "codex_7d_reset_at", resetAfterKey: "codex_7d_reset_after_seconds", interval: "7 days"},
	} {
		expression := accountUsageSortExpression(
			"accounts.extra",
			"accounts.id",
			"accounts.platform",
			"accounts.session_window_start",
			"accounts.session_window_end",
			tc.sortBy,
		)
		require.Contains(t, expression, "FROM usage_logs")
		require.Contains(t, expression, "input_tokens::bigint + output_tokens::bigint + cache_creation_tokens::bigint + cache_read_tokens::bigint")
		require.Contains(t, expression, tc.resetAtKey)
		require.Contains(t, expression, tc.resetAfterKey)
		require.Contains(t, expression, "INTERVAL '"+tc.interval+"'")
		require.Contains(t, expression, "COUNT(*) = 0 THEN NULL")
		require.Contains(t, expression, "date_trunc('hour', CURRENT_TIMESTAMP)")
		require.NotContains(t, expression, "used_percent")
	}

	require.Empty(t, accountUsageSortExpression("accounts.extra", "accounts.id", "accounts.platform", "accounts.session_window_start", "accounts.session_window_end", "name"))
}

func TestAccountUsageSortIsAppliedBeforePaginationWithMissingValuesLast(t *testing.T) {
	for _, tc := range []struct {
		sortBy   string
		order    string
		wantKey  string
		wantSort string
	}{
		{sortBy: "usage_5h", order: "desc", wantKey: "FROM usage_logs", wantSort: "DESC NULLS LAST"},
		{sortBy: "usage_7d", order: "asc", wantKey: "FROM usage_logs", wantSort: "ASC NULLS LAST"},
	} {
		var capturedSQL string
		db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(captureEntQueryMatcher{actual: &capturedSQL}))
		require.NoError(t, err)
		driver := entsql.OpenDB(dialect.Postgres, db)
		client := dbent.NewClient(dbent.Driver(driver))
		repo := newAccountRepositoryWithSQL(client, db, nil)

		mock.ExpectQuery("count").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery("list").WillReturnRows(sqlmock.NewRows(dbaccount.Columns))
		_, _, err = repo.ListWithFilters(context.Background(), pagination.PaginationParams{
			Page: 2, PageSize: 20, SortBy: tc.sortBy, SortOrder: tc.order,
		}, "", "", "", "", 0, "")
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())

		normalized := normalizeSQLWhitespace(capturedSQL)
		require.Contains(t, normalized, tc.wantKey)
		require.Contains(t, normalized, tc.wantSort)
		require.Less(t, strings.Index(normalized, " ORDER BY "), strings.Index(normalized, " LIMIT "))

		mock.ExpectClose()
		require.NoError(t, client.Close())
		require.NoError(t, mock.ExpectationsWereMet())
	}
}
