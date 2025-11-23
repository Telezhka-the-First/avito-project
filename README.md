# review_assigner avito project

Никаких проблем встречено не было. Все задания, включая все дополнительные, были выполнены.

Помимо описанных в openapi.yml также добавлена команда:

`GET /stats/assignments` — простая статистика по назначениям:
- количество назначений по пользователям;
- количество ревьюеров по каждому PR.

Результаты тестов:

![load_test](images\load_test.png)


![load_deactivate](images\load_deactivate.png)


![integration](images\integration.png)