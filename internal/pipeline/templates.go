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

	azurePipelinesGeneric = `trigger:
  branches:
    include:
      - main

pool:
  vmImage: ubuntu-latest

steps:
  - script: echo "configure CI steps for {{.Project.Name}}"
    displayName: verify
`

	azurePipelinesDotnet = `trigger:
  branches:
    include:
      - main

pool:
  vmImage: ubuntu-latest

steps:
  - task: UseDotNet@2
    displayName: Install .NET SDK
  - script: dotnet restore
    displayName: restore
  - script: dotnet build --no-restore
    displayName: build
  - script: dotnet test --no-build
    displayName: test
`

	azurePipelinesDotnetNext = `trigger:
  branches:
    include:
      - main

pool:
  vmImage: ubuntu-latest

stages:
  - stage: Verify
    jobs:
      - job: Api
        steps:
          - task: UseDotNet@2
          - script: dotnet restore && dotnet build --no-restore && dotnet test --no-build
            displayName: dotnet verify
      - job: Web
        steps:
          - task: NodeTool@0
            inputs:
              versionSpec: 22.x
          - script: corepack enable && pnpm install --frozen-lockfile && pnpm lint && pnpm typecheck && pnpm build
            displayName: web verify
`

	azurePipelinesRails = `trigger:
  branches:
    include:
      - main

pool:
  vmImage: ubuntu-latest

steps:
  - task: UseRubyVersion@0
    inputs:
      versionSpec: ">= 3.2"
  - script: bundle install && bundle exec rails test
    displayName: verify
`

	azurePipelinesFastifyNext = `trigger:
  branches:
    include:
      - main

pool:
  vmImage: ubuntu-latest

steps:
  - task: NodeTool@0
    inputs:
      versionSpec: 22.x
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
