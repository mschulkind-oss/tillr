#!/usr/bin/env node
// Format all feature specs to proper markdown

const API = 'http://localhost:9878'

const sleep = (ms) => new Promise(r => setTimeout(r, ms))

async function main() {
  const res = await fetch(`${API}/api/features`)
  const features = await res.json()

  let updated = 0
  let skipped = 0
  let failed = 0

  for (const f of features) {
    if (!f.spec || f.spec.trim() === '') {
      skipped++
      continue
    }

    const newSpec = formatSpec(f.name, f.spec)
    if (newSpec === f.spec) {
      skipped++
      continue
    }

    let success = false
    for (let attempt = 0; attempt < 3; attempt++) {
      try {
        const patchRes = await fetch(`${API}/api/features/${f.id}`, {
          method: 'PATCH',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ spec: newSpec }),
        })

        if (patchRes.ok) {
          updated++
          console.log(`✓ ${f.id}`)
          success = true
          break
        } else {
          const errText = await patchRes.text()
          if (errText.includes('SQLITE_BUSY') && attempt < 2) {
            await sleep(200 * (attempt + 1))
            continue
          }
          console.log(`✗ ${f.id}: ${patchRes.status} — ${errText.slice(0, 100)}`)
          failed++
          success = true // don't retry
          break
        }
      } catch (e) {
        if (attempt < 2) {
          await sleep(200)
          continue
        }
        console.log(`✗ ${f.id}: ${e.message}`)
        failed++
        success = true
        break
      }
    }

    // Small delay between requests to avoid SQLITE_BUSY
    await sleep(50)
  }

  console.log(`\nDone: ${updated} updated, ${skipped} skipped, ${failed} failed`)
}

function formatSpec(name, spec) {
  if (hasGoodMarkdownStructure(spec)) {
    return cleanupMarkdown(spec)
  }
  if (spec.match(/^##\s/m)) {
    return cleanupMarkdown(spec)
  }
  return convertToMarkdown(name, spec)
}

function hasGoodMarkdownStructure(spec) {
  const headerCount = (spec.match(/^#{2,4}\s/gm) || []).length
  return headerCount >= 2
}

function cleanupMarkdown(spec) {
  let text = spec
  // Normalize: ensure double newline before headers (except first line)
  text = text.replace(/([^\n])\n(#{1,4}\s)/g, '$1\n\n$2')
  // Ensure double newline after headers
  text = text.replace(/(^#{1,4}\s.+$)\n(?!\n)/gm, '$1\n\n')
  return text.trim()
}

function convertToMarkdown(name, spec) {
  const lines = spec.split('\n')

  let preambleLines = []
  let contentLines = []
  let inContent = false

  for (const line of lines) {
    const trimmed = line.trim()
    if (!inContent && trimmed.match(/^(acceptance criteria|requirements|overview|description|spec):/i)) {
      inContent = true
      continue
    }
    if (!inContent && !trimmed.match(/^\d+\.\s/) && !trimmed.match(/^[-*]\s/) && trimmed !== '' && trimmed !== '---') {
      preambleLines.push(trimmed)
    } else {
      inContent = true
      contentLines.push(line)
    }
  }

  const title = toTitleCase(name)
  let md = `## ${title}\n\n`

  if (preambleLines.length > 0) {
    md += preambleLines.join('\n') + '\n\n'
  }

  const criteria = []
  const testing = []

  for (const line of contentLines) {
    const trimmed = line.trim()
    if (trimmed === '' || trimmed === '---') continue
    const lower = trimmed.toLowerCase()
    if (lower.match(/^(?:\d+\.\s*)?(?:test[:\s]|verify\b|manual qa|qa step|assert\b)/)) {
      testing.push(trimmed)
    } else {
      criteria.push(trimmed)
    }
  }

  if (criteria.length > 0) {
    md += `### Acceptance Criteria\n\n`
    for (const item of criteria) {
      const cleaned = item.replace(/^\d+\.\s*/, '').replace(/^[-*]\s*/, '')
      md += `- ${cleaned}\n`
    }
    md += '\n'
  }

  if (testing.length > 0) {
    md += `### Testing\n\n`
    for (const item of testing) {
      const cleaned = item.replace(/^\d+\.\s*/, '').replace(/^[-*]\s*/, '')
      md += `- ${cleaned}\n`
    }
    md += '\n'
  }

  return md.trim()
}

function toTitleCase(str) {
  return str
    .replace(/[-_]/g, ' ')
    .replace(/\b\w/g, c => c.toUpperCase())
    .replace(/\b(A|An|The|And|Or|But|In|On|At|To|For|Of|With|By|From|As|Is|Was|Be|Are)\b/gi,
      (m, _, offset) => offset === 0 ? m : m.toLowerCase())
}

main().catch(console.error)
