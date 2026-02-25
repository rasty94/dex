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
	},
	"es": {
		"login_title":          "Inicia sesión en tu cuenta",
		"login_button":         "Iniciar sesión",
		"login_with":           "Iniciar sesión con",
		"username_label":       "Usuario",
		"password_label":       "Contraseña",
		"email_label":          "Correo electrónico",
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
	},
}

// GetTranslations returns the translation map for the specified language.
// It defaults to English if the language is not supported or empty.
func GetTranslations(lang string) map[string]string {
	// Normalize language code (e.g., "es-ES" -> "es")
	lang = strings.ToLower(lang)
	if idx := strings.Index(lang, "-"); idx != -1 {
		lang = lang[:idx]
	}

	if tr, ok := Translations[lang]; ok {
		return tr
	}
	// Fallback to English
	return Translations["en"]
}
