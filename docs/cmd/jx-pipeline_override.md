## jx-pipeline override

Lets you pick a step to override locally in a pipeline

***Aliases**: edit,inline*

### Usage

```
jx-pipeline override
```

### Synopsis

Lets you pick a step to override locally in a pipeline

### Examples

  # Override locally a step in a pipeline
  jx pipeline override

### Options

```
  -b, --batch-mode         Runs in batch mode without prompting for user input
  -d, --dir string         The directory to look for the .lighthouse folder (default ".")
  -h, --help               help for override
      --log-level string   Sets the logging level. If not specified defaults to $JX_LOG_LEVEL
  -p, --pipeline string    The pipeline kind and name. e.g. 'presubmit/pr' or 'postsubmit/release'. If not specified you will be prompted to choose one
  -s, --sha string         The default catalog SHA to use when resolving catalog pipelines to reuse (default "HEAD")
  -t, --trigger string     The path to the trigger file. If not specified you will be prompted to choose one
      --verbose            Enables verbose output. The environment variable JX_LOG_LEVEL has precedence over this flag and allows setting the logging level to any value of: panic, fatal, error, warn, info, debug, trace
```

### SEE ALSO

* [jx-pipeline](jx-pipeline.md)	 - commands for working with Jenkins X Pipelines

###### Auto generated by spf13/cobra on 29-Jan-2021