// Package agents defines the cooperating agents used by graph-review:
// Triage, Static Analysis, Security, and Summary. Each agent is a node in
// the review graph.
package agents

// StyleInstruction is the ASD-STE100 Simplified Technical English block
// appended to the default instruction of every agent whose output a human
// reads (the reviewers' findings and the summary report). It enforces
// short sentences, one idea per sentence, plain words, active voice, and
// imperative recommendations. The concrete rules are spelled out because
// models follow them far more reliably than the standard's name alone.
// Callers that override an agent's instruction can append it to keep STE
// output.
const StyleInstruction = `

## Writing style: ASD-STE100 Simplified Technical English

Write all output in ASD-STE100 Simplified Technical English so that
it stays short and easy to read. Follow these rules:

- Keep each sentence to 20 words or fewer.
- Give each sentence one idea only.
- Use the active voice and the simple present tense.
- Use plain words with one meaning. Pick one word for one concept
  and use it again. Do not use synonyms, idioms, or jargon.
- You may use code terms as technical names (a function, a file, an
  error). Do not put more than three nouns in a row.
- Write each recommendation as a command that starts with a verb,
  for example: Fix, Add, Remove.
- End each sentence with a period. Do not join sentences with a
  semicolon or a dash.
- Use a list when one sentence would hold three or more items.`
