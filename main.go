package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"

	_ "embed"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/reports/v1"
	"google.golang.org/api/option"
)

//go:embed templates/stats.html
var statsTemplate string

type QuotaCollector struct {
	timestamp *prometheus.Desc
	total     *prometheus.Desc
	used      *prometheus.Desc
	client    *admin.Service
}

func NewQuotaCollector(client *admin.Service) *QuotaCollector {
	return &QuotaCollector{
		timestamp: prometheus.NewDesc("google_workspace_quota_timestamp",
			"Timestamp of the quota stats",
			nil, nil,
		),
		total: prometheus.NewDesc("google_workspace_quota_bytes_total",
			"Total quota in bytes",
			nil, nil,
		),
		used: prometheus.NewDesc("google_workspace_quota_bytes_used",
			"Used quota in bytes",
			nil, nil,
		),
		client: client,
	}
}

func (c *QuotaCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.total
	ch <- c.used
}

func (c *QuotaCollector) Collect(ch chan<- prometheus.Metric) {
	t, totalQuota, usedQuota, _, err := c.fetchQuotaStats()
	if err != nil {
		slog.Error(
			"Failed to fetch quota stats",
			slog.String("err", err.Error()),
		)
		return
	}

	ch <- prometheus.MustNewConstMetric(
		c.timestamp, prometheus.GaugeValue, float64(t.Unix()),
	)
	ch <- prometheus.MustNewConstMetric(
		c.total, prometheus.GaugeValue, totalQuota*1048576,
	)
	ch <- prometheus.MustNewConstMetric(
		c.used, prometheus.GaugeValue, usedQuota*1048576,
	)
}

func (c *QuotaCollector) fetchQuotaStats() (
	time.Time, float64, float64, float64, error,
) {
	var t time.Time
	var resp *admin.UsageReports
	var err error

	for i := -1; i > -6; i-- {
		t = time.Now().AddDate(0, 0, i).UTC().Truncate(24 * time.Hour)
		date := t.Format("2006-01-02")
		resp, err = c.client.CustomerUsageReports.Get(date).Do()
		if err == nil {
			break
		}
	}
	if err != nil {
		return time.Time{}, 0, 0, 0, err
	}

	var totalQuota float64
	var usedQuota float64

	for _, param := range resp.UsageReports[0].Parameters {
		switch param.Name {
		case "accounts:total_quota_in_mb":
			totalQuota = float64(param.IntValue)
		case "accounts:used_quota_in_mb":
			usedQuota = float64(param.IntValue)
		}
	}

	percentageUsed := (usedQuota / totalQuota) * 100

	return t, totalQuota, usedQuota, percentageUsed, nil
}

func getClient(config *oauth2.Config) *http.Client {
	tokenFile := "token.json"
	token, err := loadToken(tokenFile)
	if err != nil {
		token = getTokenFromWeb(config)
		saveToken(tokenFile, token)
	}
	return config.Client(context.Background(), token)
}

func loadToken(file string) (*oauth2.Token, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	token := &oauth2.Token{}
	err = json.NewDecoder(f).Decode(token)
	return token, err
}

func getTokenFromWeb(config *oauth2.Config) *oauth2.Token {
	authURL := config.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Go to the following link in your browser:\n%s\n", authURL)

	var code string
	if _, err := fmt.Scan(&code); err != nil {
		log.Fatalf("Unable to read authorization code: %v", err)
	}

	token, err := config.Exchange(context.Background(), code)
	if err != nil {
		log.Fatalf("Unable to retrieve token from web: %v", err)
	}
	return token
}

func saveToken(file string, token *oauth2.Token) {
	fmt.Printf("Saving credential file to: %s\n", file)
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(token)
}

type QuotaStats struct {
	Date           string  // in YYYY-MM-DD
	TotalQuota     string  // in TB
	UsedQuota      string  // in TB
	PercentageUsed float64 // in percentage
}

func statsPageHanderFunc(collector *QuotaCollector) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		t, total, used, percentage, err := collector.fetchQuotaStats()
		if err != nil {
			slog.Error(
				"Failed to fetch quota stats",
				slog.String("err", err.Error()),
			)
			http.Error(
				w, "Failed to fetch quota stats",
				http.StatusInternalServerError,
			)
			return
		}

		stats := QuotaStats{
			Date:           t.Format("2006-01-02"),
			TotalQuota:     strconv.FormatFloat(total/1048576, 'f', 3, 64),
			UsedQuota:      strconv.FormatFloat(used/1048576, 'f', 3, 64),
			PercentageUsed: percentage,
		}
		renderStatsPage(w, stats)
	}
}

func renderStatsPage(w http.ResponseWriter, stats QuotaStats) {
	tmpl, err := template.New("stats").Parse(statsTemplate)
	if err != nil {
		http.Error(
			w, "Failed to parse template", http.StatusInternalServerError,
		)
		return
	}

	err = tmpl.Execute(w, stats)
	if err != nil {
		http.Error(
			w, "Failed to render template", http.StatusInternalServerError,
		)
		return
	}
}

func main() {
	b, err := os.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	config, err := google.ConfigFromJSON(
		b,
		"https://www.googleapis.com/auth/admin.reports.usage.readonly",
	)
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}

	client := getClient(config)
	srv, err := admin.NewService(
		context.Background(), option.WithHTTPClient(client),
	)
	if err != nil {
		log.Fatalf("Unable to retrieve reports Client %v", err)
	}

	collector := NewQuotaCollector(srv)
	prometheus.MustRegister(collector)

	http.HandleFunc("/", statsPageHanderFunc(collector))
	http.Handle("/metrics", promhttp.Handler())

	port := "8080"
	if v := os.Getenv("PORT"); v != "" {
		if _, e := strconv.Atoi(v); e != nil {
			slog.Error(
				"Failed to parse PORT env var",
				slog.String("err", err.Error()),
			)
		} else {
			port = v
		}
	}

	err = http.ListenAndServe(":"+port, nil)
	if err != nil {
		slog.Error(
			"Failed to start http server",
			slog.String("err", err.Error()),
		)
		os.Exit(1)
	}
}
