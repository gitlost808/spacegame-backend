# Player name filtering

The filter uses reviewed English and German terms derived from
[LDNOOBW](https://github.com/LDNOOBW/List-of-Dirty-Naughty-Obscene-and-Otherwise-Bad-Words).
See [words/NOTICE](words/NOTICE) and [words/LICENSE](words/LICENSE) for attribution
and licensing. Lists are embedded in the server binary; filtering makes no
network calls and needs no database table or runtime files. Local extremist-name
rules are maintained separately in `words/extremism.txt`.

`words/en.txt` and `words/de.txt` contain one term per line. Comments begin with
`#`. Terms match inside normalized names unless prefixed with `=`, which limits
them to a whole token or the entire compact name. Short ambiguous terms use
this conservative rule so names such as `Assassin`, `Dickinson`, `Cockpit`,
`Analyst`, and `Marschall` remain usable. Edit the lists and rebuild to change
policy. Upstream lists are a starting point, not an exhaustive moderation policy.

Matching uses Unicode compatibility decomposition and case folding, strips
combining marks and format characters, and recognizes common leetspeak
substitutions. Spaces, punctuation, and emoji act as token separators; compact
matching also catches separated letters. Accepted names retain their trimmed
original spelling, accents, and emoji in storage and responses.

`words/allow.txt` contains normalized benign fragments to exclude from substring
checks. Exceptions only mask their own span; appending another blocked term is
still rejected. Keep exceptions narrow and add tests for both benign names and
offensive combinations when updating them.

Extremist references, slogans, and group names are checked before profanity
exceptions are applied. The policy covers explicit Hitler references, Nazi
slogans, white-supremacist group names, and English/German expressions of
antisemitic violence. The reported `ADOLF HILTER 88 HEIL` name is a regression
case, including misspellings, separator tricks, and common character substitutions.

Common misspellings such as `Hilter` require an additional `Adolf`, `Heil`, or
standalone `88` cue. `Adolf`, `Heil`, ordinary names ending in `88`, and initials
such as `SS` or `HH` are not blocked by themselves. Numeric runs `1488` and `8814`,
including separated/full-width forms, are checked before leetspeak conversion;
longer numeric runs are not treated as those codes. Keeping the numeric check
separate prevents false positives such as `Julia88`.

These local policy rules are informed by the
[ADL reference on 1488](https://www.adl.org/resources/hate-symbol/1488) and the
[BfV reference on extremist symbols and slogans](https://www.verfassungsschutz.de/SharedDocs/publikationen/DE/rechtsextremismus/2022-02-rechtsextremismus-symbole-zeichen-organisationen.pdf?__blob=publicationFile&v=2).
Review new phrases and their innocent counterexamples together when extending
the rules; do not add context-dependent short codes as blanket substring bans.

This is a best-effort English/German filter. It does not cover every language,
cross-script lookalike, creative spelling, or context-dependent insult. Whole-token
rules intentionally favor fewer false positives over catching every compound.
Existing leaderboard entries are not rewritten or removed by this filter.
