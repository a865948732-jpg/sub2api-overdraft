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

func TestAccountUsageSortExpressionNormalizesCodexAndPassivePercentages(t *testing.T) {
	for _, tc := range []struct {
		sortBy     string
		codexKey   string
		passiveKey string
	}{
		{sortBy: "usage_5h", codexKey: "codex_5h_used_percent", passiveKey: "session_window_utilization"},
		{sortBy: "usage_7d", codexKey: "codex_7d_used_percent", passiveKey: "passive_usage_7d_utilization"},
	} {
		expression := accountUsageSortExpression("accounts.extra", tc.sortBy)
		require.Contains(t, expression, "COALESCE")
		require.Contains(t, expression, tc.codexKey)
		require.Contains(t, expression, tc.passiveKey)
		require.Contains(t, expression, "jsonb_typeof")
		require.Contains(t, expression, "::numeric")
		require.Contains(t, expression, "* 100")
	}

	require.Empty(t, accountUsageSortExpression("accounts.extra", "name"))
}

func TestAccountUsageSortIsAppliedBeforePaginationWithMissingValuesLast(t *testing.T) {
	for _, tc := range []struct {
		sortBy   string
		order    string
		wantKey  string
		wantSort string
	}{
		{sortBy: "usage_5h", order: "desc", wantKey: "codex_5h_used_percent", wantSort: "DESC NULLS LAST"},
		{sortBy: "usage_7d", order: "asc", wantKey: "codex_7d_used_percent", wantSort: "ASC NULLS LAST"},
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
