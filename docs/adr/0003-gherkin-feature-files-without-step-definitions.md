# Tests are Gherkin feature files executed by agents, without step definitions

What to test lives in Gherkin `.feature` files under `qa/features/`, not in
a simsquad-specific JSON format. Gherkin already provides readable
scenarios, tag-based suites with tag expressions, Scenario Outlines for data
variants and an official Go parser, and teams may already own feature files.
Unlike Cucumber there are no step definitions: workers read the steps and
carry them out on the device, which makes a scenario executable as soon as
it is written but means results are judged by an agent, not by coded
assertions. Two conventions extend plain Gherkin: an `@explore` scenario
holds an exploration charter in its description, and run settings are tags
(`@ios`, `@android`, `@persona:<name>`, `@timebox:<n>m`, `@model:<id>`).
