# Notifications on the phone

Needs a relay ([self-host-relay](self-host-relay.md)) and an account ([account and devices](account-and-devices.md)).

On the relay origin, the bootstrap page installs as an app (*Install*, or *Add to Home Screen* on iOS, which shows notifications only from the installed app). Settings > Notifications > *Enable on this device* subscribes the browser, on **every pod of the account** (a project of another pod notifies it too): a pod notifies it when the agent finishes a turn or asks for you, when a saved command flagged *test* fails, and when the assistant or Claude Code leaves feedback on a ticket — one switch per kind, *Send a test* to check. Nothing is sent while the device shows a window of that pod. A click opens the conversation or the ticket. The pod encrypts every notification for the browser: the relay forwards it without being able to read it.
