// Triage CLI for defect reports (wyrd cli_defects.py pattern):
//
//	defects list [-status new] [-limit 20]
//	defects show -id <id>
//	defects accept -id <id> [-ticket bd_x] [-note ...]
//	defects dismiss -id <id> [-note ...]
//
// Table from DEFECTS_TABLE (default 521studios-<DEFECTS_ENV|staging>-pfsrd2-defects).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/521studios/pfsrd2-data-api/internal/defects"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	if os.Getenv(defects.EnvTable) == "" {
		env := os.Getenv("DEFECTS_ENV")
		if env == "" {
			env = "staging"
		}
		os.Setenv(defects.EnvTable, fmt.Sprintf("521studios-%s-pfsrd2-defects", env))
	}
	ctx := context.Background()
	client, err := defects.NewClient(ctx)
	if err != nil {
		fatal(err)
	}

	switch os.Args[1] {
	case "list":
		fs := flag.NewFlagSet("list", flag.ExitOnError)
		status := fs.String("status", defects.StatusNew, "new|accepted|dismissed")
		limit := fs.Int("limit", 20, "max reports")
		fs.Parse(os.Args[2:])
		items, err := client.ListByStatus(ctx, *status, int32(*limit))
		if err != nil {
			fatal(err)
		}
		for _, it := range items {
			fmt.Printf("%s  %s  %-9s  %s — %s\n",
				it["id"], it["created_at"], it["status"],
				str(it["creature_name"], str(it["creature_game_id"], "?")), it["reason"])
		}
		fmt.Printf("%d report(s)\n", len(items))
	case "show":
		fs := flag.NewFlagSet("show", flag.ExitOnError)
		id := fs.String("id", "", "defect id")
		fs.Parse(os.Args[2:])
		require(*id, "-id")
		item, err := client.Get(ctx, *id)
		if err != nil {
			fatal(err)
		}
		if item == nil {
			fatal(fmt.Errorf("no defect %s", *id))
		}
		out, _ := json.MarshalIndent(item, "", "  ")
		fmt.Println(string(out))
	case "accept", "dismiss":
		fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
		id := fs.String("id", "", "defect id")
		ticket := fs.String("ticket", "", "bd ticket id (accept)")
		note := fs.String("note", "", "triage note")
		fs.Parse(os.Args[2:])
		require(*id, "-id")
		status := defects.StatusAccepted
		if os.Args[1] == "dismiss" {
			status = defects.StatusDismissed
		}
		if err := client.SetStatus(ctx, *id, status, *ticket, *note); err != nil {
			fatal(err)
		}
		fmt.Printf("%s %s\n", status, *id)
	default:
		usage()
	}
}

func str(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

func require(v, name string) {
	if v == "" {
		fatal(fmt.Errorf("%s is required", name))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: defects list|show|accept|dismiss [flags]")
	os.Exit(2)
}
