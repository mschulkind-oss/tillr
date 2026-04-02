import { useState, useCallback } from 'react'
import ReactMarkdown from 'react-markdown'
import { Lightbox } from './Lightbox'

interface MarkdownContentProps {
  children: string
  className?: string
}

/**
 * Renders markdown content with lightbox support for images.
 * Click any image to open it in a full-screen lightbox overlay.
 */
export function MarkdownContent({ children, className }: MarkdownContentProps) {
  const [lightboxSrc, setLightboxSrc] = useState<string | null>(null)
  const [lightboxAlt, setLightboxAlt] = useState<string>('')

  const openLightbox = useCallback((src: string, alt: string) => {
    setLightboxSrc(src)
    setLightboxAlt(alt)
  }, [])

  return (
    <>
      <div className={className}>
      <ReactMarkdown
        components={{
          img: ({ src, alt, ...props }) => (
            <img
              {...props}
              src={src}
              alt={alt || ''}
              className="cursor-zoom-in rounded"
              onClick={(e) => {
                e.preventDefault()
                e.stopPropagation()
                if (src) openLightbox(src, alt || '')
              }}
            />
          ),
        }}
      >
        {children}
      </ReactMarkdown>
      </div>
      {lightboxSrc && (
        <Lightbox
          src={lightboxSrc}
          alt={lightboxAlt}
          onClose={() => setLightboxSrc(null)}
        />
      )}
    </>
  )
}
