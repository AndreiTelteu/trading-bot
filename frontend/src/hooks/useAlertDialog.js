import { useCallback, useMemo, useState } from 'react'

function useAlertDialog(initialConfig = null) {
  const [dialog, setDialog] = useState(initialConfig)

  const openDialog = useCallback((config) => {
    setDialog(config)
  }, [])

  const closeDialog = useCallback(() => {
    setDialog(null)
  }, [])

  const updateDialog = useCallback((updater) => {
    setDialog((current) => {
      if (typeof updater === 'function') {
        return updater(current)
      }

      return current ? { ...current, ...updater } : updater
    })
  }, [])

  return useMemo(() => ({
    isOpen: Boolean(dialog),
    dialog,
    openDialog,
    closeDialog,
    updateDialog,
    setDialog,
  }), [dialog, openDialog, closeDialog, updateDialog])
}

export default useAlertDialog
