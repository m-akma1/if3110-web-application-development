

export const features = {
  // Resume/abort of batch migrations. On by default since the backend supports it; set
  // VITE_ENABLE_BATCH_ACTIONS=false to hide the buttons again.
  batchActions: import.meta.env.VITE_ENABLE_BATCH_ACTIONS !== 'false',
} as const