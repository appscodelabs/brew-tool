package cmds

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"

	"text/template"

	"github.com/appscodelabs/brewer/internal/git"
	"github.com/google/go-github/github"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

func NewCmdCreate() *cobra.Command {
	var brew Homebrew

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create homebrew-tap",

		Run: func(cmd *cobra.Command, args []string) {
			runCreate(brew)
		},
	}

	cmd.Flags().StringVar(&brew.Owner, "owner", "", "Current repo owner")
	cmd.Flags().StringVar(&brew.Repo, "repo", "", "Current repo name")
	cmd.Flags().StringVar(&brew.BrewOwner, "brew-owner", "appscode", "Owner of the reporitory to push the tap to")
	cmd.Flags().StringVar(&brew.BrewRepo, "brew-repo", "homebrew-tap", "Reporitory to push the tap to")
	cmd.Flags().StringVar(&brew.Author, "author", "1gtm", "Author name")
	cmd.Flags().StringVar(&brew.AuthorEmail, "email", "1gtm@appscode.com", "Author email")
	cmd.Flags().StringVar(&brew.Folder, "folder", "", "Folder inside the repository to put the formula. Default is the root folder.")
	cmd.Flags().StringVar(&brew.Caveats, "caveats", "", "Caveats for the user of your binary. Default is empty")
	cmd.Flags().StringVar(&brew.Homepage, "homepage", "https://appscode.com", "Your app's homepage.")
	cmd.Flags().StringVar(&brew.Description, "description", "", "Your app's description. Default is empty")
	cmd.Flags().BoolVar(&brew.SkipUpload, "skip-upload", false, "formula will not be published, will be stored on the dist folder only.")
	cmd.Flags().StringArrayVar(&brew.Dependencies, "dependencies", []string{}, "Packages your package depends on.")
	cmd.Flags().StringArrayVar(&brew.Conflicts, "conflicts", []string{}, "Packages that conflict with your package")

	return cmd
}

type Homebrew struct {
	Name         string
	Owner        string
	Repo         string
	BrewOwner    string
	BrewRepo     string
	Author       string
	AuthorEmail  string
	Folder       string
	Caveats      string
	Plist        string
	Install      string
	Dependencies []string
	Test         string
	Conflicts    []string
	Description  string
	Homepage     string
	SkipUpload   bool
}

func runCreate(brew Homebrew) {
	brew.Name = brew.Repo
	brew.Install = fmt.Sprintf("bin.install %s-darwin-amd64", brew.Name)

	tag, err := git.Clean(git.Run("tag", "-l", "--points-at", "HEAD"))
	if err != nil {
		log.Fatal(err)
	}

	artifact := Artifact{
		Name:    brew.Name + "-darwin-amd64",
		Path:    "dist/" + brew.Name + "/" + brew.Name + "-darwin-amd64",
		Version: tag,
	}

	content, err := buildFormula(brew, artifact)
	if err != nil {
		log.Fatal(err)
	}

	var filename = brew.Name + ".rb"
	var path = filepath.Join("dist/", filename)
	if err := ioutil.WriteFile(path, content.Bytes(), 0644); err != nil {
		log.Fatal(err)
	}

	if brew.SkipUpload {
		return
	}

	message := fmt.Sprintf("Brew formula update for %s version %s", artifact.Name, artifact.Version)
	err = upload(brew, content, brew.Name+".rb", message)
	if err != nil {
		log.Fatal(err)
	}
}

func upload(brew Homebrew, content bytes.Buffer, path, message string) error {
	//push to github
	token, found := os.LookupEnv("GH_TOOLS_TOKEN")
	if !found {
		log.Fatalln("GH_TOOLS_TOKEN env var is not set")
	}

	//github client
	ctx := context.Background()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)

	client := github.NewClient(tc)

	options := &github.RepositoryContentFileOptions{
		Committer: &github.CommitAuthor{
			Name:  github.String(brew.Author),
			Email: github.String(brew.AuthorEmail),
		},
		Content: content.Bytes(),
		Message: github.String(message),
	}

	file, _, res, err := client.Repositories.GetContents(
		ctx,
		brew.BrewOwner,
		brew.BrewRepo,
		path,
		&github.RepositoryContentGetOptions{},
	)

	if err != nil && res.StatusCode != 404 {
		return err
	}

	if res.StatusCode == 404 {
		_, _, err = client.Repositories.CreateFile(
			ctx,
			brew.BrewOwner,
			brew.BrewRepo,
			path,
			options,
		)
		return err
	}
	options.SHA = file.SHA
	_, _, err = client.Repositories.UpdateFile(
		ctx,
		brew.BrewOwner,
		brew.BrewRepo,
		path,
		options,
	)
	return err
}

func buildFormula(brew Homebrew, artifact Artifact) (bytes.Buffer, error) {
	data, err := dataFor(brew, artifact)
	if err != nil {
		return bytes.Buffer{}, err
	}
	return doBuildFormula(data)
}

func doBuildFormula(data templateData) (out bytes.Buffer, err error) {
	tmpl, err := template.New(data.Name).Parse(formulaTemplate)

	if err != nil {
		return out, err
	}
	err = tmpl.Execute(&out, data)
	return
}

func dataFor(brew Homebrew, artifact Artifact) (result templateData, err error) {
	sum, err := calculate(artifact.Path)
	if err != nil {
		return
	}
	return templateData{
		Name:         formulaNameFor(brew.Name),
		DownloadURL:  "https://github.com",
		Desc:         brew.Description,
		Homepage:     brew.Homepage,
		Owner:        brew.Owner,
		Repo:         brew.Repo,
		Tag:          artifact.Version,
		Version:      artifact.Version,
		Caveats:      split(brew.Caveats),
		File:         artifact.Name,
		SHA256:       sum,
		Dependencies: brew.Dependencies,
		Conflicts:    brew.Conflicts,
		Plist:        brew.Plist,
		Install:      split(brew.Install),
	}, nil
}
func split(s string) []string {
	strings := strings.Split(strings.TrimSpace(s), "\n")
	if len(strings) == 1 && strings[0] == "" {
		return []string{}
	}
	return strings
}

func calculate(path string) (sum string, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hash := sha256.New()

	_, err = io.Copy(hash, f)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

type Artifact struct {
	Name    string
	Path    string
	Version string
}

func formulaNameFor(name string) string {
	name = strings.Replace(name, "-", " ", -1)
	name = strings.Replace(name, "_", " ", -1)
	return strings.Replace(strings.Title(name), " ", "", -1)
}
