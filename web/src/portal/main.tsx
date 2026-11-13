import React from 'react'
import ReactDOM from 'react-dom/client'
import { PortalEntry } from './CloudPortal'
import './portal.css'

window.addEventListener('pageshow', event => {
  if (event.persisted) window.location.reload()
})

ReactDOM.createRoot(document.getElementById('portal')!).render(
  <React.StrictMode><PortalEntry /></React.StrictMode>,
)
