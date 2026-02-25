package server

import "strings"

// Translations is a map of language codes to translation maps.
var Translations = map[string]map[string]string{
	"en": {
		"login_title":          "Log in to your account",
		"login_button":         "Log in",
		"login_with":           "Log in with",
		"username_label":       "Username",
		"password_label":       "Password",
		"email_label":          "Email Address",
		"domain_label":         "Domain",
		"back_button":          "Back",
		"error_title":          "Error",
		"approval_title":       "Authorize Request",
		"approval_client":      "Client",
		"approval_permissions": "Permissions",
		"approval_approve":     "Approve",
		"approval_reject":      "Reject",
		"device_title":         "Device Login",
		"device_instructions":  "Enter the code displayed on your device.",
		"device_success_title": "Success!",
		"device_success_msg":   "You have successfully authenticated the device.",
		"oob_title":            "Login Successful",
		"oob_instructions":     "Please copy this code, switch to your application and paste it there:",
		"footer_copyright":     "© %d Dex IdP. All rights reserved.",
		// TOTP / MFA
		"totp_label":          "TOTP / Authenticator App Code",
		"totp_verify_button":  "Verify",
		"totp_invalid":        "Invalid TOTP code. Please try again.",
		"invalid_credentials": "Invalid credentials.",
		"signing_in":          "Signing in...",
	},
	"es": {
		"login_title":          "Inicia sesión en tu cuenta",
		"login_button":         "Iniciar sesión",
		"login_with":           "Iniciar sesión con",
		"username_label":       "Usuario",
		"password_label":       "Contraseña",
		"email_label":          "Correo electrónico",
		"domain_label":         "Dominio",
		"back_button":          "Atrás",
		"error_title":          "Error",
		"approval_title":       "Autorizar solicitud",
		"approval_client":      "Cliente",
		"approval_permissions": "Permisos",
		"approval_approve":     "Aprobar",
		"approval_reject":      "Rechazar",
		"device_title":         "Inicio de sesión en dispositivo",
		"device_instructions":  "Introduce el código que aparece en tu dispositivo.",
		"device_success_title": "¡Éxito!",
		"device_success_msg":   "Has autenticado el dispositivo correctamente.",
		"oob_title":            "Inicio de sesión correcto",
		"oob_instructions":     "Copia este código, vuelve a tu aplicación y pégalo allí:",
		"footer_copyright":     "© %d Dex IdP. Todos los derechos reservados.",
		// TOTP / MFA
		"totp_label":          "Código TOTP / App Autenticadora",
		"totp_verify_button":  "Verificar",
		"totp_invalid":        "Código TOTP incorrecto. Por favor, inténtalo de nuevo.",
		"invalid_credentials": "Credenciales incorrectas.",
		"signing_in":          "Iniciando sesión...",
	},
	"fr": {
		"login_title":          "Connectez-vous à votre compte",
		"login_button":         "Se connecter",
		"login_with":           "Se connecter avec",
		"username_label":       "Nom d'utilisateur",
		"password_label":       "Mot de passe",
		"email_label":          "Adresse e-mail",
		"domain_label":         "Domaine",
		"back_button":          "Retour",
		"error_title":          "Erreur",
		"approval_title":       "Autoriser la demande",
		"approval_client":      "Client",
		"approval_permissions": "Autorisations",
		"approval_approve":     "Approuver",
		"approval_reject":      "Refuser",
		"device_title":         "Connexion depuis un appareil",
		"device_instructions":  "Saisissez le code affiché sur votre appareil.",
		"device_success_title": "Succès !",
		"device_success_msg":   "Vous avez authentifié l'appareil avec succès.",
		"oob_title":            "Connexion réussie",
		"oob_instructions":     "Copiez ce code, revenez à votre application et collez-le :",
		"footer_copyright":     "© %d Dex IdP. Tous droits réservés.",
		// TOTP / MFA
		"totp_label":          "Code TOTP / Application d'authentification",
		"totp_verify_button":  "Vérifier",
		"totp_invalid":        "Code TOTP invalide. Veuillez réessayer.",
		"invalid_credentials": "Identifiants incorrects.",
		"signing_in":          "Connexion en cours...",
	},
	"de": {
		"login_title":          "Melden Sie sich an",
		"login_button":         "Anmelden",
		"login_with":           "Anmelden mit",
		"username_label":       "Benutzername",
		"password_label":       "Passwort",
		"email_label":          "E-Mail-Adresse",
		"domain_label":         "Domäne",
		"back_button":          "Zurück",
		"error_title":          "Fehler",
		"approval_title":       "Anfrage autorisieren",
		"approval_client":      "Client",
		"approval_permissions": "Berechtigungen",
		"approval_approve":     "Genehmigen",
		"approval_reject":      "Ablehnen",
		"device_title":         "Geräteanmeldung",
		"device_instructions":  "Geben Sie den auf Ihrem Gerät angezeigten Code ein.",
		"device_success_title": "Erfolg!",
		"device_success_msg":   "Sie haben das Gerät erfolgreich authentifiziert.",
		"oob_title":            "Anmeldung erfolgreich",
		"oob_instructions":     "Bitte kopieren Sie diesen Code, wechseln Sie zu Ihrer Anwendung und fügen Sie ihn dort ein:",
		"footer_copyright":     "© %d Dex IdP. Alle Rechte vorbehalten.",
		// TOTP / MFA
		"totp_label":          "TOTP / Authenticator-App-Code",
		"totp_verify_button":  "Verifizieren",
		"totp_invalid":        "Ungültiger TOTP-Code. Bitte versuchen Sie es erneut.",
		"invalid_credentials": "Ungültige Anmeldedaten.",
		"signing_in":          "Anmelden...",
	},
	"pt": {
		"login_title":          "Entrar na sua conta",
		"login_button":         "Entrar",
		"login_with":           "Entrar com",
		"username_label":       "Utilizador",
		"password_label":       "Palavra-passe",
		"email_label":          "Endereço de e-mail",
		"domain_label":         "Domínio",
		"back_button":          "Voltar",
		"error_title":          "Erro",
		"approval_title":       "Autorizar pedido",
		"approval_client":      "Cliente",
		"approval_permissions": "Permissões",
		"approval_approve":     "Aprovar",
		"approval_reject":      "Rejeitar",
		"device_title":         "Autenticação de dispositivo",
		"device_instructions":  "Introduza o código apresentado no seu dispositivo.",
		"device_success_title": "Sucesso!",
		"device_success_msg":   "Autenticou o dispositivo com sucesso.",
		"oob_title":            "Autenticação bem-sucedida",
		"oob_instructions":     "Copie este código, volte à sua aplicação e cole-o lá:",
		"footer_copyright":     "© %d Dex IdP. Todos os direitos reservados.",
		// TOTP / MFA
		"totp_label":          "Código TOTP / App Autenticadora",
		"totp_verify_button":  "Verificar",
		"totp_invalid":        "Código TOTP inválido. Por favor, tente novamente.",
		"invalid_credentials": "Credenciais inválidas.",
		"signing_in":          "A entrar...",
	},
}

// GetTranslations returns the translation map for the specified language.
// It defaults to English if the language is not supported or empty.
// The lang argument accepts Accept-Language header values like "es-ES,es;q=0.9,en;q=0.8".
func GetTranslations(lang string) map[string]string {
	// Take only the first language tag from Accept-Language header
	if idx := strings.IndexAny(lang, ",;"); idx != -1 {
		lang = lang[:idx]
	}
	// Normalize language code (e.g., "es-ES" -> "es")
	lang = strings.ToLower(strings.TrimSpace(lang))
	if idx := strings.Index(lang, "-"); idx != -1 {
		lang = lang[:idx]
	}

	if tr, ok := Translations[lang]; ok {
		return tr
	}
	// Fallback to English
	return Translations["en"]
}
