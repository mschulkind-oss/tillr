import ReactMarkdown from 'react-markdown'

interface MarkdownContentProps {
  children: string
  className?: string
}

// Renders markdown. Lightbox / image zoom removed in the reset; bring
// it back if and when an image-heavy use case appears.
export function MarkdownContent({ children, className }: MarkdownContentProps) {
  return (
    <div className={className}>
      <ReactMarkdown>{children}</ReactMarkdown>
    </div>
  )
}
