//go:build integration

package repository

import (
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (s *AccountRepoSuite) TestList_DefaultSortByNameAsc() {
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "z-account"})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "a-account"})

	accounts, _, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err)
	s.Require().Len(accounts, 2)
	s.Require().Equal("a-account", accounts[0].Name)
	s.Require().Equal("z-account", accounts[1].Name)
}

func (s *AccountRepoSuite) TestListWithFilters_SortByPriorityDesc() {
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "low-priority", Priority: 10})
	mustCreateAccount(s.T(), s.client, &service.Account{Name: "high-priority", Priority: 90})

	accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
		Page:      1,
		PageSize:  10,
		SortBy:    "priority",
		SortOrder: "desc",
	}, "", "", "", "", 0, "")
	s.Require().NoError(err)
	s.Require().Len(accounts, 2)
	s.Require().Equal("high-priority", accounts[0].Name)
	s.Require().Equal("low-priority", accounts[1].Name)
}

func (s *AccountRepoSuite) TestListWithFilters_SortByUpstreamBillingRateWithMissingLast() {
	makeAccount := func(name, status string, rate any) {
		extra := map[string]any{}
		if rate != nil {
			extra[service.UpstreamBillingProbeExtraKey] = map[string]any{
				"status": status,
				"data":   map[string]any{"effective_rate_multiplier": rate},
			}
		}
		mustCreateAccount(s.T(), s.client, &service.Account{Name: name, Extra: extra})
	}
	makeAccount("high-rate", service.UpstreamBillingProbeStatusOK, 0.8)
	makeAccount("low-rate", service.UpstreamBillingProbeStatusOK, 0.03)
	makeAccount("missing-rate", "", nil)
	makeAccount("unsupported-with-retained-rate", service.UpstreamBillingProbeStatusUnsupported, 0.01)

	for _, tc := range []struct {
		order string
		want  []string
	}{
		{order: "asc", want: []string{"low-rate", "high-rate", "missing-rate", "unsupported-with-retained-rate"}},
		{order: "desc", want: []string{"high-rate", "low-rate", "unsupported-with-retained-rate", "missing-rate"}},
	} {
		accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
			Page: 1, PageSize: 10, SortBy: "upstream_billing_rate", SortOrder: tc.order,
		}, "", "", "", "", 0, "")
		s.Require().NoError(err)
		s.Require().Len(accounts, 4)
		for i, name := range tc.want {
			s.Require().Equal(name, accounts[i].Name)
		}
	}
}

func (s *AccountRepoSuite) TestListWithFilters_SortByUsageWindowsAcrossPagesWithMissingLast() {
	accounts := map[string]*service.Account{
		"low-5h-high-7d": mustCreateAccount(s.T(), s.client, &service.Account{
			Name: "low-5h-high-7d", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		}),
		"high-5h-low-7d": mustCreateAccount(s.T(), s.client, &service.Account{
			Name: "high-5h-low-7d", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		}),
		"zero-usage": mustCreateAccount(s.T(), s.client, &service.Account{
			Name: "zero-usage", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		}),
		"missing-usage": mustCreateAccount(s.T(), s.client, &service.Account{
			Name: "missing-usage", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		}),
	}
	user := mustCreateUser(s.T(), s.client, &service.User{Email: "account-usage-sort@example.com"})
	apiKey := mustCreateApiKey(s.T(), s.client, &service.APIKey{UserID: user.ID, Key: "sk-account-usage-sort", Name: "usage-sort"})
	usageRepo := newUsageLogRepositoryWithSQL(s.client, s.repo.sql)
	now := time.Now().UTC()
	createLog := func(account *service.Account, tokens int, at time.Time, requestID string) {
		_, err := usageRepo.Create(s.ctx, &service.UsageLog{
			UserID: user.ID, APIKeyID: apiKey.ID, AccountID: account.ID,
			RequestID: requestID, Model: "gpt-5.4",
			InputTokens: tokens, CreatedAt: at,
		})
		s.Require().NoError(err)
	}

	// The sort key is the rendered window token total, not the upstream
	// percentage snapshot. Both rows are recent enough for 5h and 7d.
	createLog(accounts["low-5h-high-7d"], 10_000_000, now.Add(-1*time.Hour), "usage-sort-low-5h")
	createLog(accounts["low-5h-high-7d"], 80_000_000, now.Add(-6*time.Hour), "usage-sort-high-7d")
	createLog(accounts["high-5h-low-7d"], 50_000_000, now.Add(-1*time.Hour), "usage-sort-high-5h")
	createLog(accounts["high-5h-low-7d"], 5_000_000, now.Add(-6*time.Hour), "usage-sort-low-7d")

	// A real row with zero tokens is distinct from an account with no rows; the
	// latter must remain at the end of either sort direction.
	createLog(accounts["zero-usage"], 0, now.Add(-1*time.Hour), "usage-sort-zero")

	for _, tc := range []struct {
		sortBy string
		want   []string
	}{
		{sortBy: "usage_5h", want: []string{"high-5h-low-7d", "low-5h-high-7d", "zero-usage", "missing-usage"}},
		{sortBy: "usage_7d", want: []string{"low-5h-high-7d", "high-5h-low-7d", "zero-usage", "missing-usage"}},
	} {
		var got []string
		for page := 1; page <= 2; page++ {
			accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
				Page: page, PageSize: 2, SortBy: tc.sortBy, SortOrder: "desc",
			}, service.PlatformOpenAI, service.AccountTypeOAuth, "", "", 0, "")
			s.Require().NoError(err)
			for _, account := range accounts {
				got = append(got, account.Name)
			}
		}
		s.Require().Equal(tc.want, got)
	}
}

func (s *AccountRepoSuite) TestListWithFilters_SortByCurrentUpstreamBillingRateDuringPeak() {
	now := time.Now()
	locations := []string{"UTC", "Asia/Shanghai", "America/New_York", "Europe/London"}
	var timezone string
	var minute int
	for _, name := range locations {
		location, err := time.LoadLocation(name)
		s.Require().NoError(err)
		local := now.In(location)
		candidate := local.Hour()*60 + local.Minute()
		if candidate >= 2 && candidate <= 1436 {
			timezone = name
			minute = candidate
			break
		}
	}
	s.Require().NotEmpty(timezone)

	peakStart := fmt.Sprintf("%02d:%02d", (minute-2)/60, (minute-2)%60)
	peakEnd := fmt.Sprintf("%02d:%02d", (minute+3)/60, (minute+3)%60)
	mustCreateAccount(s.T(), s.client, &service.Account{
		Name: "current-peak-rate",
		Extra: map[string]any{
			service.UpstreamBillingProbeExtraKey: map[string]any{
				"status": service.UpstreamBillingProbeStatusOK,
				"data": map[string]any{
					"billing_scope":             "token",
					"resolved_rate_multiplier":  1.0,
					"effective_rate_multiplier": 1.0,
					"peak_rate_enabled":         true,
					"peak_start":                peakStart,
					"peak_end":                  peakEnd,
					"peak_rate_multiplier":      10.0,
					"timezone":                  timezone,
				},
			},
		},
	})
	mustCreateAccount(s.T(), s.client, &service.Account{
		Name: "current-off-peak-rate",
		Extra: map[string]any{
			service.UpstreamBillingProbeExtraKey: map[string]any{
				"status": service.UpstreamBillingProbeStatusOK,
				"data": map[string]any{
					"effective_rate_multiplier": 5.0,
				},
			},
		},
	})

	for _, tc := range []struct {
		order string
		want  []string
	}{
		{order: "asc", want: []string{"current-off-peak-rate", "current-peak-rate"}},
		{order: "desc", want: []string{"current-peak-rate", "current-off-peak-rate"}},
	} {
		accounts, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{
			Page: 1, PageSize: 10, SortBy: "upstream_billing_rate", SortOrder: tc.order,
		}, "", "", "", "", 0, "")
		s.Require().NoError(err)
		s.Require().Len(accounts, 2)
		for i, name := range tc.want {
			s.Require().Equal(name, accounts[i].Name)
		}
	}
}
