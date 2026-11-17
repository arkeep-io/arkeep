package notification

import (
	"fmt"
	"time"
)

// The emails that are not about a job: agent status, password reset and the
// SMTP test. They use the same template as job emails (see email_render.go).

// newAgentEmail decides what the email about an agent that went offline or
// came back online shows. at is when the notification was created.
func newAgentEmail(online bool, title, agentName string, at time.Time) emailContent {
	e := emailContent{Title: title, Footer: fmt.Sprintf(emailFooter, zoneLabel(at.In(emailLocation)))}
	if online {
		e.StatusLabel, e.StatusColor, e.StatusBG, e.StatusFG = "Online", greenStripe, greenBG, green
		e.Headline = "Agent back online"
		e.Summary = agentName + " reconnected."
		e.Details = []emailItem{{"Online since", formatEmailTime(at)}}
	} else {
		e.StatusLabel, e.StatusColor, e.StatusBG, e.StatusFG = "Offline", amber, amberBG, amberFG
		e.Headline = "Agent offline"
		e.Summary = agentName + " stopped responding."
		e.Details = []emailItem{{"Offline since", formatEmailTime(at)}}
	}
	e.Preheader = e.Summary
	return e
}

const resetEmailSubject = "Reset your Arkeep password"

// PasswordResetEmail returns the subject and both bodies of the email with
// the password reset link, which is valid for expiresIn. text is always set
// and holds the link: when err is not nil, the HTML body could not be
// rendered and the caller sends text alone, so that a reset never depends on
// the HTML email.
func PasswordResetEmail(link string, expiresIn time.Duration) (subject, text, html string, err error) {
	text, html, err = renderEmailBodies(newPasswordResetEmail(link, expiresIn))
	return resetEmailSubject, text, html, err
}

func newPasswordResetEmail(link string, expiresIn time.Duration) emailContent {
	expiry := "The link expires in " + formatExpiry(expiresIn) + "."
	return emailContent{
		Title:       resetEmailSubject,
		Preheader:   "Choose a new password. " + expiry,
		StatusLabel: "Account", StatusColor: orange, StatusBG: orangeBG, StatusFG: orangeFG,
		Headline:    "Reset your password",
		Summary:     "A password reset was requested for your Arkeep account.\n" + expiry,
		ActionURL:   link,
		ActionLabel: "Choose a new password",
		ActionHint:  "If the button does not work, copy this link into your browser:",
		Note:        "If you did not request this, you can ignore this email: your password will not change.",
		Footer:      emailFooterPlain,
	}
}

// formatExpiry writes d as whole hours ("1 hour", "2 hours"), or as minutes
// when it is shorter than an hour or not a whole number of hours.
func formatExpiry(d time.Duration) string {
	unit, n := "hour", int(d/time.Hour)
	if d < time.Hour || d%time.Hour != 0 {
		unit, n = "minute", int(d/time.Minute)
	}
	if n != 1 {
		unit += "s"
	}
	return fmt.Sprintf("%d %s", n, unit)
}

const testEmailSubject = "Arkeep test email"

// newTestEmail is the email of the SMTP settings test. It is a constant: no
// request-supplied value (host, port, sender) is echoed into the content.
func newTestEmail() emailContent {
	return emailContent{
		Title:       testEmailSubject,
		Preheader:   "Arkeep can send emails with these SMTP settings.",
		StatusLabel: "Test", StatusColor: orange, StatusBG: orangeBG, StatusFG: orangeFG,
		Headline: "Test email",
		Summary:  "If you can read this, Arkeep can send emails with these SMTP settings.",
		Footer:   emailFooterPlain,
	}
}
