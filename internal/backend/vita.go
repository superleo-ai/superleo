package backend

// Wellbeing is the person's health as their iPhone reads it from Apple
// Health: one summary per day (sleep, resting heart rate, HRV, activity,
// Vita's recovery and stress scores and suggestions). It is SuperLeo's own
// module now (internal/wellbeing): the phone reports to SuperLeo, and the
// tools health_* are native. It used to be the vita server, reached over MCP
// as "vita"; an mcpServers.json entry by that name is left out of the tools,
// so the same days are not offered twice under two names.

// VitaServer is the name the old vita server was configured under.
const VitaServer = "vita"

// wellbeingSection tells the model where health questions go.
func wellbeingSection() string {
	return `

The person's health is in Wellbeing (tools health_*), read from Apple Health on their iPhone, one summary per day: sleep, resting heart rate, HRV, blood oxygen, steps and activity, weight, Vita's recovery and stress scores, and its insights and suggestions. For "how did I sleep", "is my resting heart rate up", "how active was I this week" use health_today, health_days or health_trend, never memory, and answer from the numbers with their dates; health_assess judges a number against guideline ranges and health_knowledge gives evidence-based guidance. A day only has what the phone knew; a missing value is not a zero. health_today says when the phone last reported: if that is old, say so, and if nothing was ever reported, say Wellbeing on the iPhone has not been turned on. Give the numbers and what they mean, plainly; this is not medical advice, and anything worrying is for a doctor.`
}
