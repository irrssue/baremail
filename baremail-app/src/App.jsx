import { useState, useEffect } from "react"

function App() {
  const [emails, setEmails] = useState([])
  const [selected, setSelected] = useState(null)
  const [authenticated, setAuthenticated] = useState(false)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    fetch("/auth/status")
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
    fetch("/api/emails")
      .then((r) => r.json())
      .then((data) => {
        setEmails(data)
        setLoading(false)
      })
      .catch(() => setLoading(false))
  }

  function openEmail(email) {
    fetch(`/api/emails/${email.id}`)
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
              href="/auth/google"
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
        <div className="border-b border-gray-200 py-3">
          <h1
            className="text-lg font-bold cursor-pointer"
            onClick={() => setSelected(null)}
          >
            baremail
          </h1>
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
