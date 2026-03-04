import React from 'react'
import { createPortal } from 'react-dom'

function Modal({
  isOpen,
  onClose,
  children,
  overlayStyle,
  panelStyle,
  overlayClassName = '',
  panelClassName = '',
  closeOnOverlayClick = true
}) {
  if (!isOpen) return null

  const handleOverlayClick = () => {
    if (closeOnOverlayClick && onClose) {
      onClose()
    }
  }

  return createPortal(
    <div
      className={`modal-overlay glass-panel ${overlayClassName}`.trim()}
      onClick={handleOverlayClick}
      style={overlayStyle}
    >
      <div
        className={`modal-panel ${panelClassName}`.trim()}
        onClick={(e) => e.stopPropagation()}
        style={panelStyle}
      >
        {children}
      </div>
    </div>,
    document.body
  )
}

export default Modal
