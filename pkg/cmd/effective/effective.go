package effective

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jenkins-x/jx-helpers/v3/pkg/gitclient/giturl"
	"github.com/jenkins-x/jx-helpers/v3/pkg/kube/naming"
	"github.com/jenkins-x/jx-helpers/v3/pkg/scmhelpers"
	"github.com/jenkins-x/lighthouse-client/pkg/util"
	pipelinev1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1"

	"github.com/jenkins-x-plugins/jx-pipeline/pkg/lighthouses"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cmdrunner"
	"github.com/jenkins-x/jx-helpers/v3/pkg/input"
	"github.com/jenkins-x/jx-helpers/v3/pkg/input/inputfactory"
	"github.com/jenkins-x/jx-helpers/v3/pkg/options"
	"github.com/jenkins-x/jx-logging/v3/pkg/log"
	"sigs.k8s.io/yaml"

	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/helper"
	"github.com/jenkins-x/jx-helpers/v3/pkg/cobras/templates"
	"github.com/jenkins-x/jx-helpers/v3/pkg/files"
	"github.com/jenkins-x/jx-helpers/v3/pkg/termcolor"
	"github.com/jenkins-x/jx-helpers/v3/pkg/yamls"
	"github.com/jenkins-x/lighthouse-client/pkg/config/job"
	"github.com/jenkins-x/lighthouse-client/pkg/triggerconfig"
	"github.com/jenkins-x/lighthouse-client/pkg/triggerconfig/inrepo"

	"github.com/spf13/cobra"
)

// Options contains the command line options
type Options struct {
	options.BaseOptions
	lighthouses.ResolverOptions
	DiscoverScm scmhelpers.Options

	File          string
	Namespace     string
	OutFile       string
	TriggerName   string
	PipelineName  string
	Editor        string
	Line          string
	Recursive     bool
	AddDefaults   bool
	Resolver      *inrepo.UsesResolver
	Triggers      []*Trigger
	Input         input.Interface
	CommandRunner cmdrunner.CommandRunner
}

var (
	info = termcolor.ColorInfo

	cmdLong = templates.LongDesc(`
		Displays the effective tekton pipeline
`)

	cmdExample = templates.Examples(`
		# View the effective pipeline 
		jx pipeline effective

		# View the effective pipeline in VS Code 
		jx pipeline effective -e code

		# View the effective pipeline in IDEA 
		jx pipeline effective -e idea

		# Enable open in VS Code
 		export JX_EDITOR="code"
		jx pipeline effective
	`)
)

// Trigger the found trigger configs
type Trigger struct {
	Path   string
	Config *triggerconfig.Config
	Names  []string
	Paths  map[string]string
}

// NewCmdPipelineEffective creates the command
func NewCmdPipelineEffective() (*cobra.Command, *Options) {
	o := &Options{}

	cmd := &cobra.Command{
		Use:     "effective",
		Short:   "Displays the effective tekton pipeline",
		Long:    cmdLong,
		Example: cmdExample,
		Aliases: []string{"dump"},
		Run: func(_ *cobra.Command, _ []string) {
			err := o.Run()
			helper.CheckErr(err)
		},
	}

	o.ResolverOptions.AddFlags(cmd)

	cmd.Flags().StringVarP(&o.File, "file", "f", "", "The pipeline file to render")
	cmd.Flags().StringVarP(&o.TriggerName, "trigger", "t", "", "The path to the trigger file. If not specified you will be prompted to choose one")
	cmd.Flags().StringVarP(&o.PipelineName, "pipeline", "p", "", "The pipeline kind and name. e.g. 'presubmit/pr' or 'postsubmit/release'. If not specified you will be prompted to choose one")
	cmd.Flags().StringVarP(&o.OutFile, "out", "o", "", "The output file to write the effective pipeline to. If not specified output to the terminal")
	cmd.Flags().StringVarP(&o.Editor, "editor", "e", "", "The editor to open the effective pipeline inside. e.g. use 'idea' or 'code'")
	cmd.Flags().StringVarP(&o.Line, "line", "", "", "The line number to open the editor at")
	cmd.Flags().BoolVarP(&o.Recursive, "recursive", "r", false, "Recurisvely find all '.lighthouse' folders such as if linting a Pipeline Catalog")
	cmd.Flags().BoolVarP(&o.AddDefaults, "add-defaults", "", false, "Adds default parameters to the effective pipeline")

	o.BaseOptions.AddBaseFlags(cmd)
	return cmd, o
}

// Validate verifies settings
func (o *Options) Validate() error {
	err := o.BaseOptions.Validate()
	if err != nil {
		return fmt.Errorf("failed to validate base options: %w", err)
	}
	if o.Input == nil {
		o.Input = inputfactory.NewInput(&o.BaseOptions)
	}
	if o.Resolver == nil {
		o.Resolver, err = o.ResolverOptions.CreateResolver()
		if err != nil {
			return fmt.Errorf("failed to create a UsesResolver: %w", err)
		}
	}
	if o.CommandRunner == nil {
		o.CommandRunner = cmdrunner.QuietCommandRunner
	}
	if o.Editor == "" {
		o.Editor = os.Getenv("JX_EDITOR")
	}
	return nil
}

// Run implements this command
func (o *Options) Run() error {
	err := o.Validate()
	if err != nil {
		return fmt.Errorf("failed to validate options: %w", err)
	}

	if o.File != "" {
		return o.processFile()
	}
	rootDir := o.Dir

	if o.Recursive {
		err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info == nil || !info.IsDir() || info.Name() != ".lighthouse" {
				return nil
			}
			return o.ProcessDir(path)
		})
		if err != nil {
			return err
		}
	} else {
		dir := filepath.Join(rootDir, ".lighthouse")
		err := o.ProcessDir(dir)
		if err != nil {
			return err
		}
	}
	return o.processTriggers()
}

func (o *Options) ProcessDir(dir string) error {
	fs, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read dir %s: %w", dir, err)
	}
	for _, f := range fs {
		name := f.Name()
		if !f.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}

		triggerDir := filepath.Join(dir, name)
		triggersFile := filepath.Join(triggerDir, "triggers.yaml")
		exists, err := files.FileExists(triggersFile)
		if err != nil {
			return fmt.Errorf("failed to check if file exists %s: %w", triggersFile, err)
		}
		if !exists {
			continue
		}
		triggers := &triggerconfig.Config{}
		err = yamls.LoadFile(triggersFile, triggers)
		if err != nil {
			return fmt.Errorf("failed to load %s: %w", triggersFile, err)
		}
		trigger := &Trigger{
			Path:   triggersFile,
			Config: triggers,
			Paths:  make(map[string]string),
		}
		o.Triggers = append(o.Triggers, trigger)

		err = o.loadTriggerPipelines(trigger, triggerDir)
		if err != nil {
			return fmt.Errorf("failed to load pipelines for trigger: %s: %w", triggersFile, err)
		}
	}
	return nil
}

func (o *Options) loadTriggerPipelines(trigger *Trigger, dir string) error { //nolint:unparam
	repoConfig := trigger.Config
	for i := range repoConfig.Spec.Presubmits {
		r := &repoConfig.Spec.Presubmits[i]
		if r.SourcePath != "" {
			path := filepath.Join(dir, r.SourcePath)
			name := "presubmit/" + r.Name
			trigger.Names = append(trigger.Names, name)
			trigger.Paths[name] = path
		}
		if r.Agent == "" && r.PipelineRunSpec != nil {
			r.Agent = job.TektonPipelineAgent
		}
	}
	for i := range repoConfig.Spec.Postsubmits {
		r := &repoConfig.Spec.Postsubmits[i]
		if r.SourcePath != "" {
			path := filepath.Join(dir, r.SourcePath)
			name := "postsubmit/" + r.Name
			trigger.Names = append(trigger.Names, name)
			trigger.Paths[name] = path
		}
		if r.Agent == "" && r.PipelineRunSpec != nil {
			r.Agent = job.TektonPipelineAgent
		}
	}
	return nil
}

func (o *Options) processFile() error {
	path := o.File
	pr, err := lighthouses.LoadEffectivePipelineRun(o.Resolver, path)
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", path, err)
	}

	name := filepath.Base(path)
	return o.displayPipeline(path, name, pr)
}

func (o *Options) processTriggers() error {
	var names []string
	m := map[string]*Trigger{}
	for _, trigger := range o.Triggers {
		name := trigger.Path
		names = append(names, name)
		m[name] = trigger
	}

	var err error
	name := o.TriggerName
	if name == "" {
		name, err = o.Input.PickNameWithDefault(names, "pick the trigger config: ", "", "select the set of triggers to process")
		if err != nil {
			return fmt.Errorf("failed to pick trigger file: %w", err)
		}
		if name == "" {
			return fmt.Errorf("no trigger file selected")
		}
	}
	trigger := m[name]
	if trigger == nil {
		return options.InvalidOptionf("trigger", o.TriggerName, "available names %s", strings.Join(names, ", "))
	}

	pipelineName := o.PipelineName
	if pipelineName == "" {
		pipelineName, err = o.Input.PickNameWithDefault(trigger.Names, "pick the pipeline: ", "", "select the pipeline to view")
		if err != nil {
			return fmt.Errorf("failed to pick trigger file: %w", err)
		}
		if pipelineName == "" {
			return fmt.Errorf("no trigger file selected")
		}
	}

	path := trigger.Paths[pipelineName]
	if path == "" {
		return fmt.Errorf("missing trigger path for pipeline name %s", pipelineName)
	}
	pipeline, err := lighthouses.LoadEffectivePipelineRun(o.Resolver, path)
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", path, err)
	}

	return o.displayPipeline(trigger.Path, pipelineName, pipeline)
}

func (o *Options) displayPipeline(path, name string, pipeline *pipelinev1.PipelineRun) error {
	if o.AddDefaults {
		err := o.addPipelineParameterDefaults(path, pipeline)
		if err != nil {
			return fmt.Errorf("failed to : %w", err)
		}
	}

	// lets create an output file if using editor
	if o.Editor != "" && o.OutFile == "" {
		fileName := ""
		absRootDir, err := filepath.Abs(o.Dir)
		if err == nil {
			_, fileName = filepath.Split(absRootDir)
		}
		if fileName == "" || len(fileName) == 1 {
			fileName = "jx-pipeline"
		}
		tmpFileName := fileName + "-" + strings.ReplaceAll(name, string(os.PathSeparator), "-") + "-*.yaml"
		tmpFile, err := os.CreateTemp("", tmpFileName)
		if err != nil {
			return fmt.Errorf("failed to create temp file: %w", err)
		}
		o.OutFile = tmpFile.Name()
	}

	if o.OutFile != "" {
		err := yamls.SaveFile(pipeline, o.OutFile)
		if err != nil {
			return fmt.Errorf("failed to save file %s: %w", o.OutFile, err)
		}
		log.Logger().Infof("saved file %s", info(o.OutFile))

		if o.Editor != "" {
			return o.openInEditor(o.OutFile, o.Editor)
		}
		return nil
	}

	data, err := yaml.Marshal(pipeline)
	if err != nil {
		return fmt.Errorf("failed to marshal pipeline for %s: %w", name, err)
	}

	log.Logger().Infof("pipeline %s is:", info(path))
	log.Logger().Info(string(data))
	return nil
}

func (o *Options) openInEditor(path, editor string) error {
	args := []string{path}
	line := o.Line
	if line == "" {
		var err error
		line, err = findFirstStepLine(path)
		if err != nil {
			return fmt.Errorf("failed to : %w", err)
		}
		if line == "" {
			// lets guess an approximate place after all the parameters
			line = "161"
		}
	}
	if line != "" {
		switch editor {
		case "idea":
			args = []string{"--line", line, path}
		case "code":
			args = []string{"-g", path + ":" + line}
		}
	}

	c := &cmdrunner.Command{
		Name: editor,
		Args: args,
		Out:  os.Stdout,
		Err:  os.Stderr,
		In:   os.Stdin,
	}
	_, err := o.CommandRunner(c)
	if err != nil {
		return fmt.Errorf("failed to open editor via command: %s: %w", c.CLI(), err)
	}
	return nil
}

func (o *Options) addPipelineParameterDefaults(path string, pipeline *pipelinev1.PipelineRun) error {
	ps := &pipeline.Spec

	dscm := &o.DiscoverScm
	if dscm.Dir == "" {
		dscm.Dir = o.Dir
		if dscm.Dir == "" {
			dscm.Dir = filepath.Dir(path)
		}
	}

	// lets look for jenkins env vars
	if dscm.SourceURL == "" {
		dscm.SourceURL = os.Getenv("GIT_URL")
		log.Logger().Infof("GIT_URL = %s", dscm.SourceURL)

		if dscm.SourceURL == "" {
			dscm.DiscoverFromGit = true
		}
	}
	log.Logger().Infof("SourceURL = %s", dscm.SourceURL)

	dscm.IgnoreMissingToken = true
	err := dscm.Validate()
	if err != nil {
		return fmt.Errorf("failed to discover repository details: %w", err)
	}

	if dscm.Branch == "" {
		dscm.Branch = os.Getenv("PULL_BASE_REF")
	}
	if dscm.Branch == "" {
		br := os.Getenv("GIT_BRANCH")
		if br != "" {
			names := strings.Split(br, "/")
			dscm.Branch = names[len(names)-1]
		}
	}
	buildNumber := os.Getenv("BUILD_NUMBER")
	if buildNumber == "" {
		buildNumber = os.Getenv("BUILD_ID")
	}
	sha := os.Getenv("PULL_PULL_SHA")
	if sha == "" {
		sha = os.Getenv("GIT_COMMIT")
	}
	jobName := os.Getenv("JOB_NAME")

	if dscm.SourceURL == "" {
		return options.MissingOption("git-url")
	}
	if dscm.GitURL == nil {
		dscm.GitURL, err = giturl.ParseGitURL(dscm.SourceURL)
		if err != nil {
			return fmt.Errorf("failed to parse git url %s: %w", dscm.SourceURL, err)
		}
		if dscm.GitURL == nil {
			return fmt.Errorf("failed to parse the git URL")
		}
	}

	for i := range ps.Params {
		pa := &ps.Params[i]
		if string(pa.Value.Type) == "" {
			pa.Value.Type = pipelinev1.ParamTypeString
		}
		if pa.Value.StringVal == "" {
			switch pa.Name {
			case "REPO_URL":
				pa.Value.StringVal = dscm.SourceURL
			case "REPO_OWNER":
				pa.Value.StringVal = dscm.GitURL.Organisation
			case "REPO_NAME":
				pa.Value.StringVal = dscm.GitURL.Name
			case "BUILD_ID":
				pa.Value.StringVal = buildNumber
			case "PULL_BASE_REF":
				pa.Value.StringVal = dscm.Branch
			case "PULL_PULL_SHA":
				pa.Value.StringVal = sha
			case "JOB_NAME":
				pa.Value.StringVal = jobName
			}
		}
	}

	// lets add the labels/annotations
	if pipeline.Annotations == nil {
		pipeline.Annotations = map[string]string{}
	}
	if pipeline.Labels == nil {
		pipeline.Labels = map[string]string{}
	}
	if dscm.GitURL != nil {
		pipeline.Labels[util.OrgLabel] = dscm.GitURL.Organisation
		pipeline.Labels[util.RepoLabel] = dscm.GitURL.Name
	}
	if dscm.Branch != "" {
		pipeline.Labels[util.BranchLabel] = dscm.Branch
	}
	if sha != "" {
		pipeline.Labels[util.LastCommitSHALabel] = sha
	}
	if buildNumber != "" {
		pipeline.Labels["build"] = buildNumber
	}

	// lets specify the name using the build number
	if jobName == "" {
		if dscm.GitURL == nil {
			// we can't default the job name
			if pipeline.GenerateName == "" {
				name := pipeline.Name
				if name == "" {
					name = "release"
				}
				pipeline.GenerateName = name + "-"
				pipeline.Name = ""
			}
			return nil
		}
		jobName = dscm.GitURL.Organisation + "-" + dscm.Repository
	}

	if buildNumber != "" {
		pipeline.Name = naming.ToValidName(jobName + "-" + buildNumber)
	} else {
		pipeline.Name = ""
		pipeline.GenerateName = naming.ToValidName(jobName) + "-"
	}
	return nil
}

func findFirstStepLine(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to load pipeline file %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "steps:" {
			return strconv.Itoa(i + 2), nil
		}
	}
	log.Logger().Infof("could not find line with 'steps:'")
	return "", nil
}
