package main

import (
	"bytes"
	"errors"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/1Password/shell-plugins/sdk/schema"
	"github.com/AlecAivazis/survey/v2"
)

func main() {
	err := newPlugin()
	if err != nil {
		log.Fatal(err)
	}
}

func newPlugin() error {
	var questionnaire = []*survey.Question{
		{
			Name:     "Name",
			Prompt:   &survey.Input{Message: `Plugin name (e.g. "aws" or "github") [required]`},
			Validate: survey.Required,
		},
		{
			Name:     "PlatformName",
			Prompt:   &survey.Input{Message: `Platform name (e.g. "AWS" or "GitHub") [required]`},
			Validate: survey.Required,
		},
		{
			Name:   "Executable",
			Prompt: &survey.Input{Message: `Executable name (e.g. "aws" or "gh")`},
		},
		{
			Name:   "CredentialName",
			Prompt: &survey.Input{Message: `Name of the credential type (e.g. "Access Key" or "Personal Access Token")`},
			Validate: func(ans interface{}) error {
				if str, ok := ans.(string); ok {
					hasUpper := false
					for _, char := range str {
						if unicode.IsUpper(char) {
							hasUpper = true
						}
					}
					if !hasUpper {
						return errors.New(`credential name must be titlecased, e.g. "Access Key" or "Personal Access Token"`)
					}
					return nil
				}

				return nil
			},
		},
		{
			Name:   "ExampleCredential",
			Prompt: &survey.Input{Message: `Paste in an example credential`},
		},
	}

	result := struct {
		Name              string
		PlatformName      string
		Executable        string
		CredentialName    string
		ExampleCredential string

		// Derived
		PlatformNameUpperCamelCase   string
		ValueComposition             schema.ValueComposition
		FieldName                    string
		CredentialEnvVarName         string
		CredentialNameUpperCamelCase string
		CredentialNameSnakeCase      string
	}{}

	err := survey.Ask(questionnaire, &result)
	if err != nil {
		return err
	}

	if result.ExampleCredential != "" {
		result.ValueComposition = getValueComposition(result.ExampleCredential)
	}

	result.PlatformNameUpperCamelCase = strings.ReplaceAll(result.PlatformName, " ", "")

	credNameSplit := strings.Split(result.CredentialName, " ")

	result.CredentialNameUpperCamelCase = strings.Join(credNameSplit, "")
	result.CredentialNameSnakeCase = strings.ToLower(strings.Join(credNameSplit, "_"))
	result.CredentialEnvVarName = strings.ToUpper(result.Name + "_" + result.CredentialNameSnakeCase)

	// As a placeholder, assume the field name is the last word of the credential name, e.g. "Token"
	result.FieldName = credNameSplit[len(credNameSplit)-1]

	relativeDirPath := filepath.Join("plugins", result.Name)
	err = os.MkdirAll(relativeDirPath, 0777)
	if err != nil {
		return err
	}

	templates := []Template{pluginTemplate}
	if result.CredentialName != "" {
		templates = append(templates, credentialTemplate)
	}
	if result.Executable != "" {
		templates = append(templates, executableTemplate)
	}

	for _, tmpl := range templates {
		filenameTemplate, err := template.New("filename").Parse(tmpl.Filename)
		if err != nil {
			return err
		}

		var filenameBuf bytes.Buffer
		err = filenameTemplate.Execute(&filenameBuf, result)
		if err != nil {
			return err
		}
		filename := filenameBuf.String()

		contentsTemplate, err := template.New(filename).Parse(tmpl.Contents)
		if err != nil {
			return err
		}

		var contentsBuf bytes.Buffer
		err = contentsTemplate.Execute(&contentsBuf, result)
		if err != nil {
			return err
		}
		contents := contentsBuf.Bytes()

		err = os.WriteFile(filepath.Join(relativeDirPath, filename), contents, 0666)
		if err != nil {
			return err
		}
	}

	return nil
}

func getValueComposition(value string) schema.ValueComposition {
	vc := schema.ValueComposition{
		Length: len(value),
	}

	for _, r := range value {
		if unicode.IsUpper(r) {
			vc.Charset.Uppercase = true
			continue
		}
		if unicode.IsLower(r) {
			vc.Charset.Lowercase = true
			continue
		}
		if unicode.IsDigit(r) {
			vc.Charset.Digits = true
			continue
		}
		if unicode.IsSymbol(r) {
			vc.Charset.Symbols = true
			continue
		}
	}

	vc.Prefix = getPossibleTokenPrefix(value)

	return vc
}

// getPossibleTokenPrefix tries to determine if the passed in value has a token prefix, as made popular by GitHub.
// Examples of such a prefix are `github_pat_`, `gph_`, `dop_v1_`, `glpat-`. See the code comments below for
// how that's determined.
func getPossibleTokenPrefix(value string) string {
	// If the value is shorter than 25 chars, it's unlikely to be a token that can contain a prefix.
	if len(value) < 20 {
		return ""
	}

	// A token prefix is likely to not be longer than 15 characters.
	substr := []rune(value)[:15]

	// A token prefix is likely to start with a lowercase char.
	if !unicode.IsLower(substr[0]) {
		return ""
	}

	// Trim all trailing chars until a delimiter is found, or else return an empty string.
	// A delimiter can be either an underscore (_) or a dash (-).
	return strings.TrimRightFunc(string(substr), func(r rune) bool {
		return r != '_' && r != '-'
	})
}

type Template struct {
	Filename string
	Contents string
}

var pluginTemplate = Template{
	Filename: "plugin.go",
	Contents: `package {{ .Name }}

import (
	"github.com/1Password/shell-plugins/sdk"
	"github.com/1Password/shell-plugins/sdk/schema"
)

func New() schema.Plugin {
	return schema.Plugin{
		Name: "{{ .Name }}",
		Platform: schema.PlatformInfo{
			Name:     "{{ .PlatformName }}",
			Homepage: sdk.URL("https://{{ .Name }}.com"), // TODO: Check if this is correct
		},
		{{- if .CredentialName }}
		Credentials: []schema.CredentialType{
			{{ .CredentialNameUpperCamelCase }}(),
		},
		{{- end }}
		{{- if .Executable }}
		Executables: []schema.Executable{
			Executable_{{ .Executable }}(),
		},
		{{- end }}
	}
}
`,
}

var credentialTemplate = Template{
	Filename: "{{ .CredentialNameSnakeCase }}.go",
	Contents: `package {{ .Name }}

import (
	"github.com/1Password/shell-plugins/sdk"
	"github.com/1Password/shell-plugins/sdk/importer"
	"github.com/1Password/shell-plugins/sdk/provision"
	"github.com/1Password/shell-plugins/sdk/schema"
	"github.com/1Password/shell-plugins/sdk/schema/credname"
	"github.com/1Password/shell-plugins/sdk/schema/fieldname"
)

func {{ .CredentialNameUpperCamelCase }}() schema.CredentialType {
	return schema.CredentialType{
		Name:          credname.{{ .CredentialNameUpperCamelCase }},
		DocsURL:       sdk.URL("https://{{ .Name }}.com/docs/{{ .CredentialNameSnakeCase }}"), // TODO: Replace with actual URL
		ManagementURL: sdk.URL("https://console.{{ .Name }}.com/user/security/tokens"), // TODO: Replace with actual URL
		Fields: []schema.CredentialField{
			{
				Name:                fieldname.{{ .FieldName }},
				MarkdownDescription: "{{ .FieldName }} used to authenticate to {{ .PlatformName }}.",
				Secret:              true,
				{{- if .ValueComposition.Length }}
				Composition: &schema.ValueComposition{
					{{- if .ValueComposition.Length }}
					Length: {{ .ValueComposition.Length }},
					{{- end }}
					{{- if .ValueComposition.Prefix }}
					Prefix: "{{ .ValueComposition.Prefix }}", // TODO: Check if this is correct
					{{- end }}
					Charset: schema.Charset{
						{{- if .ValueComposition.Charset.Uppercase }}
						Uppercase: true,
						{{- end }}
						{{- if .ValueComposition.Charset.Lowercase }}
						Lowercase: true,
						{{- end }}
						{{- if .ValueComposition.Charset.Digits }}
						Digits:    true,
						{{- end }}
						{{- if .ValueComposition.Charset.Symbols }}
						Symbols:   true,
						{{- end }}
					},
				},
				{{- end }}
			},
		},
		Provisioner: provision.EnvVars(defaultEnvVarMapping),
		Importer: importer.TryAll(
			importer.TryEnvVarPair(defaultEnvVarMapping),
			Try{{ .PlatformNameUpperCamelCase }}ConfigFile(),
		)}
}

var defaultEnvVarMapping = map[string]string{
	fieldname.{{ .FieldName }}: "{{ .CredentialEnvVarName }}",
}

func Try{{ .PlatformNameUpperCamelCase }}ConfigFile() sdk.Importer { // TODO: See if function name should be more specific
	return importer.TryFile("~/path/to/config/file", func(ctx context.Context, contents importer.FileContents, in sdk.ImportInput, out *sdk.ImportOutput) {
		// TODO: Write importer that tries to find a {{ .CredentialName }} in the platform's config file, if such a concept exists.
	})
}
`,
}

var executableTemplate = Template{
	Filename: "{{ .Executable }}.go",
	Contents: `package {{ .Name }}

import (
	"github.com/1Password/shell-plugins/sdk"
	"github.com/1Password/shell-plugins/sdk/needsauth"
	"github.com/1Password/shell-plugins/sdk/schema"
)

func Executable_{{ .Executable }}() schema.Executable {
	return schema.Executable{
		Runs:      []string{"{{ .Executable }}"},
		Name:      "{{ .PlatformName }} CLI", // TODO: Check if this is correct
		DocsURL:   sdk.URL("https://{{ .Name }}.com/docs/cli"), // TODO: Replace with actual URL
		NeedsAuth: needsauth.NotForHelpOrVersion(),
		{{- if .CredentialName }}
		Credentials: []schema.CredentialType{
			{{ .CredentialNameUpperCamelCase }}(),
		},
		{{- end }}
	}
}
`,
}