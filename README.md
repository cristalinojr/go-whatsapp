Nossos serviços utilizam API Rest da aplicação go-whatsapp-rest (dimaskiddo no github)
que por sua vez utiliza a lib go-whatsapp (Rhymen no github). Criamos um fork (cristalinojr) de um outro fork (tulir).

Se for preciso alterar (ou consultar) a versão do WhatsAppWeb rapidamente:

No projeto go-whatsapp-rest, localizar a pasta "vendor/github.com/cristalinojr/go-whatsapp", arquivo session.go 
e alterar onde vê-se o número "3324" no exemplo abaixo:

//represents the WhatsAppWeb client version
var waVersion = []int{2, 2045, 15}

Para achar a versão: No WhatsApp Web, apertar nos três pontos verticais → Configurações → Ajuda → Versão

Isso vai ser alterado no fork do projeto em "cristalinojr" da lib whatsapp-go. Será necessário criar uma nova release do go-whatsapp-rest após o push. E depois disso rodar o deploy no Jenkins.

O serviço da api está hospedado no futurepages.org, possui serviços no Jenkins de atualização (Deploy WhatsApp) da versão e de restart (que é feito automaticamente todo dia de madrugada).

O código puxado é o hospedado em nosso GitLab (git@gitlab.com:workset/go-whatsapp-rest.git), um clone com um branch atrasado "convitin".

Não estamos mais recebendo novidades do "go-whatsapp-rest", pois quando tentamos atualizar no branch "atualizacao" ao testar na aplicação convite.in, quebrou a lógica, pelo jeito o autor modificou bastante o funcionamento do serviço. Tudo indica que teremos que readaptar a nossa ferramenta, o que não se mostrou muito simples ao tentarmos no final de 2019. O branch "convitin" é portanto o nosso branch que tá no ar.

Aparentemente o que mudou diz respeito à forma de expirar o token do QR-Code. Antes o comportamento era bem diferente. A forma como era norteou todo o desenvolvimento do RSVP Go na nossa ferramenta, portanto, foi concluído que iremos atualizar somente quando for necessário acrescentar algo destas novas funcionalidades da api rest do dimaskiddo.
