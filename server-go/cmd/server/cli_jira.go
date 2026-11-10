package main

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/jackc/pgx/v5/pgxpool"

	"example/zeitprotokoll-server/internal/app"
)

const jiraUsage = `Aufruf:
  jira list                                         Jira-Instanzen zeigen
  jira add <name> <https://adresse> [--tempo dc|none|cloud] [--kind dc|cloud] [--off]
                                                    Instanz anlegen (Standard: dc, Tempo dc, eingeschaltet)
  jira set <id|name> [--name N] [--url U] [--tempo T] [--on|--off] [--same-system]
                                                    ändern; neue Adresse (Host) löscht NUR die Tokens dieser Instanz;
                                                    mit Verweisen auf Buchungen nur mit --same-system (dasselbe Jira, neue Adresse)
  jira remove <id|name> [--force]                   löschen (mit Verweisen auf Buchungen nur mit --force; sonst --off)
Jira Cloud / Tempo Cloud lassen sich anlegen, aber noch nicht einschalten (folgt).
`

func flagVal(args []string, name string) *string {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == name {
			v := args[i+1]
			return &v
		}
	}
	return nil
}

func cmdJira(ctx context.Context, a *app.App, pool *pgxpool.Pool, args []string) int {
	if len(args) == 0 {
		fmt.Print(jiraUsage)
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		return 1
	}
	onOff := func(args []string) *bool {
		var b *bool
		if has(args, "--on") {
			t := true
			b = &t
		}
		if has(args, "--off") {
			f := false
			b = &f
		}
		return b
	}
	switch args[0] {
	case "list":
		list, err := a.JiraInstances(ctx)
		if err != nil {
			return fail(err)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tNAME\tART\tTEMPO\tAN\tADRESSE")
		for _, i := range list {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%v\t%s\n", i.ID, i.Name, i.Kind, i.Tempo, i.Enabled, i.BaseURL)
		}
		tw.Flush()
		return 0
	case "add":
		if len(args) < 3 {
			fmt.Print(jiraUsage)
			return 2
		}
		on := true
		if has(args, "--off") {
			on = false
		}
		in := app.JiraInstanceInput{Name: &args[1], BaseURL: &args[2], Tempo: flagVal(args, "--tempo"), Kind: flagVal(args, "--kind"), Enabled: &on}
		i, err := a.JiraInstanceCreate(ctx, in)
		if err != nil {
			return fail(err)
		}
		audit(ctx, pool, "cli.jira", fmt.Sprintf("angelegt #%d %s", i.ID, i.Name))
		fmt.Printf("OK: #%d %s\n", i.ID, i.Name)
		return 0
	case "set":
		if len(args) < 2 {
			fmt.Print(jiraUsage)
			return 2
		}
		cur, err := a.JiraInstanceFind(ctx, args[1])
		if err != nil {
			return fail(err)
		}
		in := app.JiraInstanceInput{Name: flagVal(args, "--name"), BaseURL: flagVal(args, "--url"), Tempo: flagVal(args, "--tempo"), Enabled: onOff(args), SameSystem: has(args, "--same-system")}
		i, cleared, err := a.JiraInstanceUpdate(ctx, cur.ID, in)
		if err != nil {
			return fail(err)
		}
		audit(ctx, pool, "cli.jira", fmt.Sprintf("geändert #%d %s (%d Tokens gelöscht)", i.ID, i.Name, cleared))
		fmt.Printf("OK: #%d %s", i.ID, i.Name)
		if cleared > 0 {
			fmt.Printf(" – Adresse gewechselt, %d Tokens gelöscht (Personen tragen sie neu ein)", cleared)
		}
		fmt.Println()
		return 0
	case "remove":
		if len(args) < 2 {
			fmt.Print(jiraUsage)
			return 2
		}
		cur, err := a.JiraInstanceFind(ctx, args[1])
		if err != nil {
			return fail(err)
		}
		_, tokens, err := a.JiraInstanceDelete(ctx, cur.ID, has(args, "--force"))
		if err != nil {
			return fail(err)
		}
		audit(ctx, pool, "cli.jira", fmt.Sprintf("gelöscht #%d %s (%d Tokens)", cur.ID, cur.Name, tokens))
		fmt.Printf("OK: #%d %s gelöscht, %d Tokens entfernt\n", cur.ID, cur.Name, tokens)
		return 0
	}
	fmt.Print(jiraUsage)
	return 2
}
