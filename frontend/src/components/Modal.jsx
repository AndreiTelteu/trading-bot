import React, { useEffect } from 'react'
import { createPortal } from 'react-dom'

function Modal({
  isOpen,
  onClose,
  children,
  overlayStyle,
  panelStyle,
  overlayClassName = '',
  panelClassName = '',
  closeOnOverlayClick = true,
  hideCloseButton = false,
}) {
  useEffect(() => {
    if (!isOpen) {
      return undefined
    }

    const handleKeyDown = (event) => {
      if (event.key === 'Escape' && onClose) {
        onClose()
      }
    }

    window.addEventListener('keydown', handleKeyDown)

    return () => {
      window.removeEventListener('keydown', handleKeyDown)
    }
  }, [isOpen, onClose])

  if (!isOpen) return null

  const handleOverlayClick = () => {
    if (closeOnOverlayClick && onClose) {
      onClose()
    }
  }

  return createPortal(
    <div
      className={`modal-overlay ${overlayClassName}`.trim()}
      style={overlayStyle}
    >
      <button
        type="button"
        className="modal-backdrop"
        onClick={handleOverlayClick}
        aria-label="Close modal"
      />
      <div
        className={`modal-panel ${panelClassName}`.trim()}
        role="dialog"
        aria-modal="true"
        style={panelStyle}
      >
        {!hideCloseButton && onClose && (
          <button
            type="button"
            className="modal-close"
            onClick={onClose}
            aria-label="Close modal"
          >
            ×
          </button>
        )}
        {children}
      </div>
    </div>,
    document.body
  )
}

export default Modal
