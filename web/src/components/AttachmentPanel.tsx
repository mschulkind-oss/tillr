import { useQuery, useQueryClient } from '@tanstack/react-query'
import { getAttachments, uploadAttachment, deleteAttachment, attachmentUrl } from '../api/client'
import type { Attachment } from '../api/types'
import { useState, useRef, useCallback } from 'react'
import { formatTimestamp } from '../lib/utils'
import { useStore } from '../store'

interface AttachmentPanelProps {
  entityType: string
  entityId: string
}

export function AttachmentPanel({ entityType, entityId }: AttachmentPanelProps) {
  const queryClient = useQueryClient()
  const addToast = useStore((s) => s.addToast)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [uploading, setUploading] = useState(false)
  const [lightboxSrc, setLightboxSrc] = useState<string | null>(null)

  const attachments = useQuery({
    queryKey: ['attachments', entityType, entityId],
    queryFn: () => getAttachments(entityType, entityId),
    enabled: !!entityId,
  })

  const handleUpload = useCallback(async (files: FileList | File[]) => {
    setUploading(true)
    try {
      for (const file of Array.from(files)) {
        await uploadAttachment(entityType, entityId, file)
      }
      queryClient.invalidateQueries({ queryKey: ['attachments', entityType, entityId] })
      addToast('Attachment uploaded', 'success')
    } catch (err) {
      addToast(`Upload failed: ${err instanceof Error ? err.message : 'unknown error'}`, 'error')
    } finally {
      setUploading(false)
      if (fileInputRef.current) fileInputRef.current.value = ''
    }
  }, [entityType, entityId, queryClient, addToast])

  const handleDelete = useCallback(async (att: Attachment) => {
    try {
      await deleteAttachment(att.filename)
      queryClient.invalidateQueries({ queryKey: ['attachments', entityType, entityId] })
      addToast('Attachment deleted', 'success')
    } catch (err) {
      addToast(`Delete failed: ${err instanceof Error ? err.message : 'unknown error'}`, 'error')
    }
  }, [entityType, entityId, queryClient, addToast])

  // Handle paste events for image upload
  const handlePaste = useCallback((e: React.ClipboardEvent) => {
    const items = e.clipboardData?.items
    if (!items) return
    const imageFiles: File[] = []
    for (const item of Array.from(items)) {
      if (item.type.startsWith('image/')) {
        const file = item.getAsFile()
        if (file) imageFiles.push(file)
      }
    }
    if (imageFiles.length > 0) {
      e.preventDefault()
      handleUpload(imageFiles)
    }
  }, [handleUpload])

  const isImage = (ct?: string) => ct?.startsWith('image/')

  const data = attachments.data || []

  return (
    <div
      className="bg-bg-card border border-border rounded-lg p-5"
      onPaste={handlePaste}
      tabIndex={-1}
    >
      <div className="flex items-center justify-between mb-3">
        <h2 className="text-sm font-semibold text-text-primary">Attachments</h2>
        <button
          onClick={() => fileInputRef.current?.click()}
          disabled={uploading}
          className="text-xs px-3 py-1.5 rounded border border-border-light hover:border-accent/40 hover:bg-accent/5 text-text-secondary hover:text-accent transition-colors disabled:opacity-50"
        >
          {uploading ? 'Uploading...' : 'Attach'}
        </button>
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          multiple
          className="hidden"
          onChange={(e) => e.target.files && handleUpload(e.target.files)}
        />
      </div>

      {data.length === 0 && !attachments.isLoading && (
        <p className="text-xs text-text-muted">No attachments yet. Click Attach or paste an image.</p>
      )}

      {data.length > 0 && (
        <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-3">
          {data.map((att) => (
            <AttachmentThumb
              key={att.id}
              att={att}
              onView={() => isImage(att.content_type) && setLightboxSrc(attachmentUrl(att.filename))}
              onDelete={() => handleDelete(att)}
            />
          ))}
        </div>
      )}

      {/* Lightbox */}
      {lightboxSrc && (
        <div
          className="fixed inset-0 z-50 bg-black/80 flex items-center justify-center cursor-pointer"
          onClick={() => setLightboxSrc(null)}
        >
          <img
            src={lightboxSrc}
            alt="Attachment preview"
            className="max-w-[90vw] max-h-[90vh] rounded shadow-xl"
            onClick={(e) => e.stopPropagation()}
          />
        </div>
      )}
    </div>
  )
}

function AttachmentThumb({ att, onView, onDelete }: { att: Attachment; onView: () => void; onDelete: () => void }) {
  const isImg = att.content_type?.startsWith('image/')
  const url = attachmentUrl(att.filename)

  return (
    <div className="group relative bg-bg-secondary border border-border-light rounded-lg overflow-hidden">
      {isImg ? (
        <div
          className="aspect-square cursor-pointer overflow-hidden"
          onClick={onView}
        >
          <img
            src={url}
            alt={att.label || att.original_name}
            className="w-full h-full object-cover group-hover:scale-105 transition-transform"
            loading="lazy"
          />
        </div>
      ) : (
        <a
          href={url}
          target="_blank"
          rel="noopener noreferrer"
          className="aspect-square flex items-center justify-center bg-bg-tertiary"
        >
          <span className="text-2xl">📄</span>
        </a>
      )}
      <div className="p-2">
        <p className="text-xs text-text-primary truncate" title={att.original_name}>
          {att.label || att.original_name}
        </p>
        <p className="text-[10px] text-text-muted">{formatTimestamp(att.created_at)}</p>
      </div>
      <button
        onClick={(e) => { e.stopPropagation(); onDelete() }}
        className="absolute top-1 right-1 opacity-0 group-hover:opacity-100 text-xs bg-danger/80 text-white rounded px-1.5 py-0.5 transition-opacity hover:bg-danger"
        title="Delete attachment"
      >
        x
      </button>
    </div>
  )
}
