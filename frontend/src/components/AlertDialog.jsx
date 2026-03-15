import React, { useEffect, useMemo, useRef } from 'react'
import { createPortal } from 'react-dom'

const TONE_LABELS = {
  info: 'Notice',
  success: 'Success',
  warning: 'Warning',
  danger: 'Alert',
}

function AlertDialog({
  isOpen,
  onClose,
  title,
  message,
  description,
  tone = 'info',
  icon,
  buttons = [],
  children,
  closeOnOverlayClick = true,
  hideCloseButton = false,
  overlayClassName = '',
  panelClassName = '',
}) {
  const actionRefs = useRef([])
  const autoFocusIndex = useMemo(
    () => buttons.findIndex((button) => button.autoFocus),
    [buttons]
  )

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

  useEffect(() => {
    if (!isOpen || autoFocusIndex < 0) {
      return
    }

    const button = actionRefs.current[autoFocusIndex]
    if (button) {
      button.focus()
    }
  }, [autoFocusIndex, isOpen])

  if (!isOpen) return null

  const resolvedTone = TONE_LABELS[tone] ? tone : 'info'
  const defaultIcon = {
    info: 'i',
    success: 'OK',
    warning: '!',
    danger: '!!',
  }[resolvedTone]

  const handleOverlayClick = () => {
    if (closeOnOverlayClick && onClose) {
      onClose()
    }
  }

  const handleAction = (button) => {
    if (button.onClick) {
      button.onClick()
    }

    if (button.closeOnClick && onClose) {
      onClose()
    }
  }

  return createPortal(
    <div className={`alert-dialog-overlay ${overlayClassName}`.trim()}>
      <button
        type="button"
        className="alert-dialog-backdrop"
        onClick={handleOverlayClick}
        aria-label="Close alert dialog"
      />
      <div
        className={`alert-dialog-panel glass-panel alert-dialog-tone-${resolvedTone} ${panelClassName}`.trim()}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="alert-dialog-title"
        aria-describedby={message || description ? 'alert-dialog-message' : undefined}
      >
        {!hideCloseButton && onClose && (
          <button
            type="button"
            className="alert-dialog-close"
            onClick={onClose}
            aria-label="Close alert"
          >
            ×
          </button>
        )}

        <div className="alert-dialog-header">
          <div className={`alert-dialog-icon alert-dialog-icon-${resolvedTone}`} aria-hidden="true">
            {icon || defaultIcon}
          </div>
          <div className="alert-dialog-heading">
            <div className={`alert-dialog-tone-label alert-dialog-tone-label-${resolvedTone}`}>
              {TONE_LABELS[resolvedTone]}
            </div>
            <h2 id="alert-dialog-title" className="alert-dialog-title">
              {title}
            </h2>
          </div>
        </div>

        {(message || description || children) && (
          <div className="alert-dialog-body">
            {message && (
              <p id="alert-dialog-message" className="alert-dialog-message">
                {message}
              </p>
            )}
            {description && <p className="alert-dialog-description">{description}</p>}
            {children ? <div className="alert-dialog-content">{children}</div> : null}
          </div>
        )}

        {buttons.length > 0 && (
          <div className="alert-dialog-actions">
            {buttons.map((button, index) => {
              const variant = button.variant || 'secondary'
              const className =
                variant === 'danger'
                  ? 'btn-danger'
                  : variant === 'primary'
                    ? 'btn-primary'
                    : variant === 'ghost'
                      ? 'btn-ghost'
                      : 'btn-secondary'

              return (
                <button
                  key={`${button.label}-${index}`}
                  type="button"
                  className={className}
                  onClick={() => handleAction(button)}
                  disabled={button.disabled}
                  ref={(element) => {
                    actionRefs.current[index] = element
                  }}
                >
                  {button.label}
                </button>
              )
            })}
          </div>
        )}
      </div>
    </div>,
    document.body
  )
}

export default AlertDialog
