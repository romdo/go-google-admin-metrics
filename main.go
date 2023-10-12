package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"os"

	_ "embed"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	admin "google.golang.org/api/admin/reports/v1"
)

//go:embed templates/stats.html
var statsTemplate string

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
	f, err := os.OpenFile(file, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		log.Fatalf("Unable to cache oauth token: %v", err)
	}
	defer f.Close()
	json.NewEncoder(f).Encode(token)
}

type QuotaStats struct {
	TotalQuota     string  // in TB
	UsedQuota      string  // in TB
	PercentageUsed float64 // in percentage
}

func renderStatsPage(w http.ResponseWriter, stats QuotaStats) {
	tmpl, err := template.New("stats").Parse(statsTemplate)
	if err != nil {
		http.Error(w, "Failed to parse template", http.StatusInternalServerError)
		return
	}

	err = tmpl.Execute(w, stats)
	if err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
		return
	}
}

func main() {
	b, err := ioutil.ReadFile("credentials.json")
	if err != nil {
		log.Fatalf("Unable to read client secret file: %v", err)
	}

	config, err := google.ConfigFromJSON(b, "https://www.googleapis.com/auth/admin.reports.usage.readonly")
	if err != nil {
		log.Fatalf("Unable to parse client secret file to config: %v", err)
	}

	client := getClient(config)
	srv, err := admin.New(client)
	if err != nil {
		log.Fatalf("Unable to retrieve reports Client %v", err)
	}

	getQuotaStats := func() (string, string, float64) {
		resp, err := srv.CustomerUsageReports.Get("2023-10-10").Do() // Replace with the appropriate date
		if err != nil {
			log.Fatalf("Unable to get report: %v", err)
		}

		totalQuota := 0
		usedQuota := 0

		for _, param := range resp.UsageReports[0].Parameters {
			switch param.Name {
			case "accounts:total_quota_in_mb":
				totalQuota = int(param.IntValue)
			case "accounts:used_quota_in_mb":
				usedQuota = int(param.IntValue)
			}
		}

		totalQuotaTB := float64(totalQuota) / 1048576
		usedQuotaTB := float64(usedQuota) / 1048576
		percentageUsed := (usedQuotaTB / totalQuotaTB) * 100

		return fmt.Sprintf("%.3f", totalQuotaTB), fmt.Sprintf("%.3f", usedQuotaTB), percentageUsed
	}

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		totalQuota, usedQuota, percentageUsed := getQuotaStats()
		stats := QuotaStats{
			TotalQuota:     totalQuota,
			UsedQuota:      usedQuota,
			PercentageUsed: percentageUsed,
		}
		renderStatsPage(w, stats)
	})

	http.ListenAndServe(":8080", nil)
}
