package repos

import (
	"fmt"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/cli/cli/v2/pkg/cmdutil"
	"github.com/cli/cli/v2/pkg/iostreams"
	"github.com/cli/cli/v2/pkg/search"
	"github.com/cli/cli/v2/pkg/text"
	"github.com/cli/cli/v2/utils"
	"github.com/spf13/cobra"
)

const (
	// Limitation of GitHub search see:
	// https://docs.github.com/en/rest/reference/search
	searchMaxResults = 1000
)

type ReposOptions struct {
	Browser  cmdutil.Browser
	Exporter cmdutil.Exporter
	IO       *iostreams.IOStreams
	Query    search.Query
	Searcher search.Searcher
	WebMode  bool
}

func NewCmdRepos(f *cmdutil.Factory, runF func(*ReposOptions) error) *cobra.Command {
	var order string
	var sort string
	opts := &ReposOptions{
		Browser: f.Browser,
		IO:      f.IOStreams,
		Query:   search.Query{Kind: search.KindRepositories},
	}

	cmd := &cobra.Command{
		Use:   "repos [<query>]",
		Short: "Search for repositories",
		Long: heredoc.Doc(`
			Search for repositories on GitHub.

			The command supports constructing queries using the GitHub search syntax,
			using the parameter and qualifier flags, or a combination of the two.

			GitHub search syntax is documented at:
			https://docs.github.com/search-github/searching-on-github/searching-for-repositories
    `),
		Example: heredoc.Doc(`
			# search repositories matching set of keywords "cli" and "shell"
			$ gh search repos cli shell

			# search repositories matching phrase "vim plugin"
			$ gh search repos "vim plugin"

			# search repositories public repos in the microsoft organization
			$ gh search repos --owner=microsoft --visibility=public

			# search repositories with a set of topics
			$ gh search repos --topic=unix,terminal

			# search repositories by coding language and number of good first issues
			$ gh search repos --language=go --good-first-issues=">=10"
    `),
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 0 && c.Flags().NFlag() == 0 {
				return cmdutil.FlagErrorf("specify search keywords or flags")
			}
			if opts.Query.Limit < 1 || opts.Query.Limit > searchMaxResults {
				return cmdutil.FlagErrorf("`--limit` must be between 1 and 1000")
			}
			if c.Flags().Changed("order") {
				opts.Query.Order = order
			}
			if c.Flags().Changed("sort") {
				opts.Query.Sort = sort
			}
			opts.Query.Keywords = args
			if runF != nil {
				return runF(opts)
			}
			var err error
			opts.Searcher, err = searcher(f)
			if err != nil {
				return err
			}
			return reposRun(opts)
		},
	}

	// Output flags
	cmdutil.AddJSONFlags(cmd, &opts.Exporter, search.RepositoryFields)
	cmd.Flags().BoolVarP(&opts.WebMode, "web", "w", false, "Open the search query in the web browser")

	// Query parameter flags
	cmd.Flags().IntVarP(&opts.Query.Limit, "limit", "L", 30, "Maximum number of repositories to fetch")
	cmdutil.StringEnumFlag(cmd, &order, "order", "", "desc", []string{"asc", "desc"}, "Order of repositories returned, ignored unless '--sort' flag is specified")
	cmdutil.StringEnumFlag(cmd, &sort, "sort", "", "best-match", []string{"forks", "help-wanted-issues", "stars", "updated"}, "Sort fetched repositories")

	// Query qualifier flags
	cmdutil.NilBoolFlag(cmd, &opts.Query.Qualifiers.Archived, "archived", "", "Filter based on archive state")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.Created, "created", "", "Filter based on created at `date`")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.Followers, "followers", "", "Filter based on `number` of followers")
	cmdutil.StringEnumFlag(cmd, &opts.Query.Qualifiers.Fork, "include-forks", "", "", []string{"false", "true", "only"}, "Include forks in fetched repositories")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.Forks, "forks", "", "Filter on `number` of forks")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.GoodFirstIssues, "good-first-issues", "", "Filter on `number` of issues with the 'good first issue' label")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.HelpWantedIssues, "help-wanted-issues", "", "Filter on `number` of issues with the 'help wanted' label")
	cmdutil.StringSliceEnumFlag(cmd, &opts.Query.Qualifiers.In, "match", "", nil, []string{"name", "description", "readme"}, "Restrict search to specific field of repository")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.Language, "language", "", "Filter based on the coding language")
	cmd.Flags().StringSliceVar(&opts.Query.Qualifiers.License, "license", nil, "Filter based on license type")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.Org, "owner", "", "Filter on owner")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.Pushed, "updated", "", "Filter on last updated at `date`")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.Size, "size", "", "Filter on a size range, in kilobytes")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.Stars, "stars", "", "Filter on `number` of stars")
	cmd.Flags().StringSliceVar(&opts.Query.Qualifiers.Topic, "topic", nil, "Filter on topic")
	cmd.Flags().StringVar(&opts.Query.Qualifiers.Topics, "number-topics", "", "Filter on `number` of topics")
	cmdutil.StringEnumFlag(cmd, &opts.Query.Qualifiers.Is, "visibility", "", "", []string{"public", "private", "internal"}, "Filter based on visibility")

	return cmd
}

func reposRun(opts *ReposOptions) error {
	io := opts.IO
	if opts.WebMode {
		url := opts.Searcher.URL(opts.Query)
		if io.IsStdoutTTY() {
			fmt.Fprintf(io.ErrOut, "Opening %s in your browser.\n", utils.DisplayURL(url))
		}
		return opts.Browser.Browse(url)
	}
	io.StartProgressIndicator()
	result, err := opts.Searcher.Repositories(opts.Query)
	io.StopProgressIndicator()
	if err != nil {
		return err
	}
	if err := io.StartPager(); err == nil {
		defer io.StopPager()
	} else {
		fmt.Fprintf(io.ErrOut, "failed to start pager: %v\n", err)
	}
	if opts.Exporter != nil {
		return opts.Exporter.Write(io, result.Items)
	}
	return displayResults(io, result)
}

func displayResults(io *iostreams.IOStreams, results search.RepositoriesResult) error {
	cs := io.ColorScheme()
	tp := utils.NewTablePrinter(io)
	for _, repo := range results.Items {
		tags := []string{repo.Visibility}
		if repo.IsFork {
			tags = append(tags, "fork")
		}
		if repo.IsArchived {
			tags = append(tags, "archived")
		}
		info := strings.Join(tags, ", ")
		infoColor := cs.Gray
		if repo.IsPrivate {
			infoColor = cs.Yellow
		}
		tp.AddField(repo.FullName, nil, cs.Bold)
		description := repo.Description
		tp.AddField(text.ReplaceExcessiveWhitespace(description), nil, nil)
		tp.AddField(info, nil, infoColor)
		if tp.IsTTY() {
			tp.AddField(utils.FuzzyAgoAbbr(time.Now(), repo.UpdatedAt), nil, cs.Gray)
		} else {
			tp.AddField(repo.UpdatedAt.Format(time.RFC3339), nil, nil)
		}
		tp.EndRow()
	}
	if io.IsStdoutTTY() {
		header := "No repositories matched your search\n"
		if len(results.Items) > 0 {
			header = fmt.Sprintf("Showing %d of %d repositories\n\n", len(results.Items), results.Total)
		}
		fmt.Fprintf(io.Out, "\n%s", header)
	}
	return tp.Render()
}

func searcher(f *cmdutil.Factory) (search.Searcher, error) {
	cfg, err := f.Config()
	if err != nil {
		return nil, err
	}
	host, err := cfg.DefaultHost()
	if err != nil {
		return nil, err
	}
	client, err := f.HttpClient()
	if err != nil {
		return nil, err
	}
	return search.NewSearcher(client, host), nil
}
