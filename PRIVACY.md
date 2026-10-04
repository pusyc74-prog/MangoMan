# MangoMan privacy policy

Last updated: 4 October 2026

MangoMan is free software that runs on your own computer. This page says
what happens to your data when you use it.

## What stays on your computer

- **Your keys.** Provider keys are kept in your computer's keychain or in an
  encrypted file on your computer. They are only ever sent to the provider
  they belong to.
- **Your usage log.** MangoMan records which model answered, whether it
  worked, how long it took and how many tokens it used. It never records
  your prompts or the answers.
- **Your settings.** My list, groups and other settings are stored in a file
  on your computer.

MangoMan has no account system, no analytics and no tracking. We do not
receive your prompts, answers, files or keys.

## What goes to AI providers

When you ask something, MangoMan sends your request straight from your
computer to one of the AI providers you connected (for example Groq,
Cerebras, NVIDIA, OpenRouter or OpenCode Zen). That provider's own terms and
privacy policy apply to what you send.

**Free models come at a cost: some providers use what you send them to
train their AI models.** This includes most free models on OpenCode Zen and
OpenRouter, and may change at any time. Do not send passwords, ID numbers,
bank details, health records or other private information through free
models. To avoid a provider, turn it off in the dashboard or remove its key.
Ollama runs models on your own computer, so nothing leaves it.

## Advanced agents

Agents from the marketplace run on your computer in a sandbox. An agent can
only reach the internet addresses it declares, and the dashboard shows them
before you install it. An agent marked "developer cloud" sends your data to
its developer; its listing says what they do with it.

## Other connections MangoMan makes

- It reads each connected provider's public list of models, to find new free
  models. This sends your key to that provider, as any request does.
- It downloads the marketplace list from GitHub when you browse agents.

## Your choices

- Remove a key: `mangoman keys rm PROVIDER`, or Remove in the dashboard.
- Delete everything: delete MangoMan's settings folder. It is
  `%APPDATA%\mangoman` on Windows, `~/Library/Application Support/mangoman`
  on a Mac and `~/.config/mangoman` on Linux.

## Changes and questions

When this policy changes, the date at the top changes too. Questions or
complaints: open an issue on the MangoMan GitHub repository.
