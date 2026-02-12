import { useState } from "react"

const emails = [
  { id: 1, sender: "alice@company.com", name: "Alice Johnson", subject: "Project update for Q1", body: "Hi,\n\nJust wanted to share the latest numbers from Q1. Revenue is up 12% and we're on track to hit our targets. Let me know if you'd like to discuss further.\n\nBest,\nAlice" },
  { id: 2, sender: "bob@company.com", name: "Bob Smith", subject: "Meeting tomorrow at 10am", body: "Hey,\n\nDon't forget we have a sync tomorrow at 10am in the main conference room. Please bring your laptop.\n\nThanks,\nBob" },
  { id: 3, sender: "charlie@billing.com", name: "Charlie Lee", subject: "Invoice #4821 attached", body: "Hello,\n\nPlease find invoice #4821 attached for the services rendered in January. Payment is due within 30 days.\n\nRegards,\nCharlie" },
  { id: 4, sender: "dana@dev.io", name: "Dana White", subject: "Quick question about the API", body: "Hi,\n\nI noticed the /users endpoint returns a 500 when the query param is empty. Is that expected behavior or a bug?\n\nThanks,\nDana" },
  { id: 5, sender: "eve@hr.com", name: "Eve Martinez", subject: "Welcome to the team!", body: "Welcome aboard!\n\nWe're thrilled to have you join us. Your onboarding session is scheduled for Monday at 9am. Please bring a valid ID.\n\nCheers,\nEve" },
  { id: 6, sender: "frank@ops.com", name: "Frank Ocean", subject: "Re: Deployment schedule", body: "Sounds good. Let's push the deployment to Thursday evening to avoid peak traffic. I'll update the runbook.\n\n- Frank" },
  { id: 7, sender: "grace@eng.com", name: "Grace Hopper", subject: "Bug report - login page", body: "Hi,\n\nUsers on Safari are reporting that the login button is unresponsive after the latest release. I've attached a screen recording.\n\nBest,\nGrace" },
  { id: 8, sender: "hank@news.com", name: "Hank Green", subject: "Newsletter: February edition", body: "Hi reader,\n\nHere's what's new this month: we launched three features, fixed a dozen bugs, and welcomed two new team members. Read more on our blog.\n\n- Hank" },
]

function App() {
  const [selected, setSelected] = useState(null)

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
              <div><span className="text-gray-500">To:</span> irrssue@gmail.com</div>
            </div>
            <hr className="my-4 border-gray-200" />
            <div className="whitespace-pre-line">{selected.body}</div>
          </div>
        ) : (
          <div>
            {emails.map((email) => (
              <div
                key={email.id}
                onClick={() => setSelected(email)}
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
