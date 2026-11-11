// Package network asks a chain's public REST (LCD) endpoints which app
// version the network runs and whether an upgrade is scheduled.
package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Plan struct {
	Name   string `json:"name"`
	Height string `json:"height"`
	Info   string `json:"info"`
}

type Info struct {
	Endpoint   string
	AppVersion string
	Plan       *Plan // nil when no upgrade is scheduled or the query failed
}

// Query tries each endpoint in order and returns the first that answers.
func Query(ctx context.Context, client *http.Client, endpoints []string) (Info, error) {
	if len(endpoints) == 0 {
		return Info{}, errors.New("the registry lists no REST endpoints for this chain")
	}
	var errs []string
	for _, ep := range endpoints {
		ep = strings.TrimRight(ep, "/")
		var ni struct {
			ApplicationVersion struct {
				Version string `json:"version"`
			} `json:"application_version"`
		}
		if err := getJSON(ctx, client, ep+"/cosmos/base/tendermint/v1beta1/node_info", &ni); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ep, err))
			continue
		}
		if ni.ApplicationVersion.Version == "" {
			errs = append(errs, ep+": node_info has no application version")
			continue
		}
		info := Info{Endpoint: ep, AppVersion: ni.ApplicationVersion.Version}
		var cp struct {
			Plan *Plan `json:"plan"`
		}
		if getJSON(ctx, client, ep+"/cosmos/upgrade/v1beta1/current_plan", &cp) == nil {
			info.Plan = cp.Plan
		}
		return info, nil
	}
	return Info{}, fmt.Errorf("no REST endpoint answered:\n  %s", strings.Join(errs, "\n  "))
}

func getJSON(ctx context.Context, client *http.Client, url string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
