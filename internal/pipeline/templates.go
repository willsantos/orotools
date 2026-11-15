package pipeline

const (
	githubActionsGeneric = `name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Verify
        run: echo "configure CI steps for {{.Project.Name}}"
`

	githubActionsDotnet = `name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-dotnet@v4
      - run: dotnet restore
      - run: dotnet build --no-restore
      - run: dotnet test --no-build
`

	githubActionsDotnetNext = `name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  api:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-dotnet@v4
      - run: dotnet restore
      - run: dotnet build --no-restore
      - run: dotnet test --no-build

  web:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: pnpm
      - run: pnpm install --frozen-lockfile
      - run: pnpm lint
      - run: pnpm typecheck
      - run: pnpm build
`

	githubActionsRails = `name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: ruby/setup-ruby@v1
        with:
          bundler-cache: true
      - run: bundle install
      - run: bundle exec rails test
`

	githubActionsFastifyNext = `name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: pnpm/action-setup@v4
      - uses: actions/setup-node@v4
        with:
          node-version: 22
          cache: pnpm
      - run: pnpm install --frozen-lockfile
      - run: pnpm lint
      - run: pnpm typecheck
      - run: pnpm test
      - run: pnpm build
`

	azurePipelinesGeneric = `# CI padrão Oroborus (gerado pelo OroTools). Layout: .azure-pipelines/ci.yml.
# PR: em Azure Repos o pr: do YAML não dispara. Configure Build Validation nas
# policies das branches main e dev (tools/apply_branch_policies.py, do
# oroborus-engineering-standards).
name: $(Date:yyyy.MM.dd).$(Rev:r)

trigger:
  branches:
    include:
      - main
      - dev

pr: none

pool:
  name: Default

steps:
  - checkout: self
    persistCredentials: false
  - script: echo "configure CI steps for {{.Project.Name}}"
    displayName: verify
`

	azurePipelinesDotnet = `# CI padrão Oroborus (gerado pelo OroTools). Layout: .azure-pipelines/ci.yml.
# PR: em Azure Repos o pr: do YAML não dispara. Configure Build Validation nas
# policies das branches main e dev (tools/apply_branch_policies.py, do
# oroborus-engineering-standards).
name: $(Date:yyyy.MM.dd).$(Rev:r)

trigger:
  branches:
    include:
      - main
      - dev

pr: none

pool:
  name: Default

variables:
  DOTNET_VERSION: "10.x"

steps:
  - checkout: self
    persistCredentials: false
  - task: UseDotNet@2
    displayName: Install .NET SDK
    inputs:
      packageType: sdk
      version: $(DOTNET_VERSION)
  - script: dotnet restore
    displayName: restore
  - script: dotnet build --no-restore
    displayName: build
  - script: dotnet test --no-build
    displayName: test
`

	azurePipelinesDotnetNext = `# CI padrão Oroborus (gerado pelo OroTools). Layout: .azure-pipelines/ci.yml.
# PR: em Azure Repos o pr: do YAML não dispara. Configure Build Validation nas
# policies das branches main e dev (tools/apply_branch_policies.py, do
# oroborus-engineering-standards).
name: $(Date:yyyy.MM.dd).$(Rev:r)

trigger:
  branches:
    include:
      - main
      - dev

pr: none

pool:
  name: Default

variables:
  DOTNET_VERSION: "10.x"
  NODE_VERSION: "24.x"

stages:
  - stage: Verify
    jobs:
      - job: Api
        steps:
          - checkout: self
            persistCredentials: false
          - task: UseDotNet@2
            displayName: Install .NET SDK
            inputs:
              packageType: sdk
              version: $(DOTNET_VERSION)
          - script: dotnet restore && dotnet build --no-restore && dotnet test --no-build
            displayName: dotnet verify
      - job: Web
        steps:
          - checkout: self
            persistCredentials: false
          - task: NodeTool@0
            inputs:
              versionSpec: $(NODE_VERSION)
          - script: corepack enable && pnpm install --frozen-lockfile && pnpm lint && pnpm typecheck && pnpm build
            displayName: web verify
`

	azurePipelinesRails = `# CI padrão Oroborus (gerado pelo OroTools). Layout: .azure-pipelines/ci.yml.
# PR: em Azure Repos o pr: do YAML não dispara. Configure Build Validation nas
# policies das branches main e dev (tools/apply_branch_policies.py, do
# oroborus-engineering-standards).
name: $(Date:yyyy.MM.dd).$(Rev:r)

trigger:
  branches:
    include:
      - main
      - dev

pr: none

pool:
  name: Default

variables:
  RUBY_VERSION: ">= 3.2"

steps:
  - checkout: self
    persistCredentials: false
  - task: UseRubyVersion@0
    inputs:
      versionSpec: $(RUBY_VERSION)
  - script: bundle install && bundle exec rails test
    displayName: verify
`

	azurePipelinesFastifyNext = `# CI padrão Oroborus (gerado pelo OroTools). Layout: .azure-pipelines/ci.yml.
# PR: em Azure Repos o pr: do YAML não dispara. Configure Build Validation nas
# policies das branches main e dev (tools/apply_branch_policies.py, do
# oroborus-engineering-standards).
name: $(Date:yyyy.MM.dd).$(Rev:r)

trigger:
  branches:
    include:
      - main
      - dev

pr: none

pool:
  name: Default

variables:
  NODE_VERSION: "24.x"

steps:
  - checkout: self
    persistCredentials: false
  - task: NodeTool@0
    inputs:
      versionSpec: $(NODE_VERSION)
  - script: corepack enable && pnpm install --frozen-lockfile && pnpm lint && pnpm typecheck && pnpm test && pnpm build
    displayName: verify
`
)

var embeddedDefaults = map[string]map[string]string{
	"github-actions": {
		"generic":       githubActionsGeneric,
		"dotnet":        githubActionsDotnet,
		"dotnet-next":   githubActionsDotnetNext,
		"rails":         githubActionsRails,
		"fastify-next":  githubActionsFastifyNext,
		"test-recipe":   githubActionsGeneric,
	},
	"azure-pipelines": {
		"generic":       azurePipelinesGeneric,
		"dotnet":        azurePipelinesDotnet,
		"dotnet-next":   azurePipelinesDotnetNext,
		"rails":         azurePipelinesRails,
		"fastify-next":  azurePipelinesFastifyNext,
		"test-recipe":   azurePipelinesGeneric,
	},
}

func defaultTemplate(providerKey, recipeName string) (string, bool) {
	byRecipe, ok := embeddedDefaults[providerKey]
	if !ok {
		return "", false
	}
	if tmpl, ok := byRecipe[recipeName]; ok {
		return tmpl, true
	}
	return "", false
}
