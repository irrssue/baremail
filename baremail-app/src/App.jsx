import { useState, useEffect } from "react"

const API = import.meta.env.VITE_API_URL || ""

function App() {
  const [emails, setEmails] = useState([])
  const [selected, setSelected] = useState(null)
  const [authenticated, setAuthenticated] = useState(false)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    fetch(`${API}/auth/status`, { credentials: "include" })
      .then((r) => r.json())
      .then((data) => {
        setAuthenticated(data.authenticated)
        if (data.authenticated) loadEmails()
        else setLoading(false)
      })
      .catch(() => setLoading(false))
  }, [])

  function loadEmails() {
    setLoading(true)
    fetch(`${API}/api/emails`, { credentials: "include" })
      .then((r) => r.json())
      .then((data) => {
        setEmails(data)
        setLoading(false)
      })
      .catch(() => setLoading(false))
  }

  function openEmail(email) {
    fetch(`${API}/api/emails/${email.id}`, { credentials: "include" })
      .then((r) => r.json())
      .then((data) => setSelected(data))
  }

  if (loading) {
    return (
      <div className="min-h-screen bg-white text-black">
        <div className="mx-auto w-[48vw]">
          <div className="border-b border-gray-200 py-3">
            <h1 className="text-lg font-bold">baremail</h1>
          </div>
          <div className="py-8 text-gray-400">Loading...</div>
        </div>
      </div>
    )
  }

  if (!authenticated) {
    return (
      <div className="min-h-screen bg-white text-black">
        <div className="mx-auto w-[48vw]">
          <div className="border-b border-gray-200 py-3">
            <h1 className="text-lg font-bold">baremail</h1>
          </div>
          <div className="py-8">
            <a
              href={`${API}/auth/google`}
              className="text-sm text-blue-600 hover:underline"
            >
              Sign in with Google
            </a>
          </div>
        </div>
      </div>
    )
  }

  return (
    <div className="min-h-screen bg-white text-black">
      <div className="mx-auto w-[48vw]">
        <div className="border-b border-gray-200 py-3 flex items-center justify-between">
          <h1
            className="text-lg font-bold cursor-pointer"
            onClick={() => setSelected(null)}
          >
            baremail
          </h1>
          <button
            onClick={() =>
              fetch(`${API}/auth/logout`, { credentials: "include" }).then(() => {
                setAuthenticated(false)
                setEmails([])
                setSelected(null)
              })
            }
            className="text-xs text-gray-400 hover:text-gray-700"
          >
            sign out
          </button>
        </div>
        {selected ? (
          <div className="py-4">
            <div className="text-sm cursor-pointer text-gray-500 mb-4" onClick={() => setSelected(null)}>
              &larr; back
            </div>
            <div className="space-y-1">
              <div><span className="text-gray-500">From:</span> {selected.sender}</div>
              <div><span className="text-gray-500">Subject:</span> {selected.subject}</div>
              <div><span className="text-gray-500">To:</span> {selected.to}</div>
            </div>
            <hr className="my-4 border-gray-200" />
            <div className="whitespace-pre-line">{selected.body || selected.snippet}</div>
          </div>
        ) : (
          <div>
            {emails.map((email) => (
              <div
                key={email.id}
                onClick={() => openEmail(email)}
                className="grid grid-cols-[200px_1fr] border-b border-gray-100 py-3 cursor-pointer hover:bg-gray-50"
              >
                <span className="font-medium truncate">{email.name}</span>
                <span className="truncate text-gray-700">{email.subject}</span>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

export default App
